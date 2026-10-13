# Continuous integration

Workflow: `.github/workflows/ci.yml` — runs on every push (all branches), on `v*` tags, on pull requests from forks and on demand (*Run workflow*).

```
changes ──┬─▶ lint       gofmt · go vet · staticcheck · govulncheck        (Go changed)
          ├─▶ test       unit + integration on PostgreSQL 16, -race, coverage (Go changed)
          ├─▶ terraform  fmt · init · validate                            (Terraform changed)
          └─▶ image      Docker build (buildx cache) → GHCR on master / tags (Go or Docker changed)
secrets     gitleaks on the full history                                   (always)
CI OK       single result: every job succeeded or was skipped by the filters
```

## Why it is fast and cheap

| Measure | Effect |
|---|---|
| Path filters (`dorny/paths-filter`) | a README / wiki change runs only *secrets* + *CI OK* (~20 s) |
| Jobs in parallel; *image* waits for *lint* + *test* | no image is built from code that fails |
| Go module and build cache (`setup-go`), Docker layer cache (`type=gha`) | incremental builds |
| `concurrency` group | a new push cancels the run in progress on the same branch (never on `master`) |
| Same-repository pull requests skipped | the push already ran the same checks |
| Tools pinned (`staticcheck@v0.8.2`, `govulncheck@v1.9.0`) | reproducible, no surprise upgrade |
| Dependabot (weekly, grouped) | one PR per ecosystem: Go modules, Actions, Docker, Terraform |

Typical durations: *lint* ~2 min, *test* ~3 min (race detector), *terraform* ~10 s, *image* ~1–2 min with a warm cache.

## How to check a result

1. **Repository page** — the icon next to the last commit (🟢 / 🔴 / 🟡); click it for the list of checks.
2. **Actions tab** → workflow **CI** → the run of your commit or branch.
   - The graph shows each job; a skipped job (grey) was not needed by the path filters.
   - **Summary** of the run: *Test coverage* table (total + service / store / web packages) and the `coverage` artifact (7 days).
   - A red job → click it → the failing step is expanded; `govulncheck` prints the vulnerable module and the fixed version, `gofmt` the files to format.
3. **Pull request** — the *Checks* section; make **CI OK** the only required check in *Settings → Branches → Branch protection* (it is green when every job passed or was legitimately skipped).
4. **Container image** — after a merge on `master` or a `v*` tag: *Packages* on the repository page → `gcp-gca-lic-mgmt`, tags `sha-xxxxxxx`, `master`, `latest`, `1.2.3`.
   ```bash
   docker run --rm -p 8080:8080 ghcr.io/francoisgn/gcp-gca-lic-mgmt:latest   # needs a DATABASE_URL, see Local Development
   ```
5. **Wiki** — after a merge touching `docs/wiki/`, the *Publish wiki* workflow mirrors it to the wiki (Actions → *Publish wiki*).

## Reproduce locally

```bash
make lint                       # gofmt + go vet
go run honnef.co/go/tools/cmd/staticcheck@v0.8.2 ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
make db && make test-int        # same tests as CI, race detector on
terraform -chdir=deploy/terraform fmt -check -recursive
```

## Typical failures

| Job | Message | Fix |
|---|---|---|
| lint / govulncheck | `Vulnerability #n … Fixed in: module@vX` | `go get module@vX && go mod tidy` (or Go patch release: CI already uses the latest 1.26.x) |
| lint / gofmt | `gofmt needed on: …` | `gofmt -w .` |
| test | `TEST_DATABASE_URL` / connection refused | PostgreSQL service not healthy: re-run once |
| secrets | `leaks found` | remove the secret, **rotate it**, rewrite the commit if not pushed to `master`; a false positive goes in `.gitleaksignore` (fingerprint) or gets an inline `# gitleaks:allow` |
| image | push denied | *Settings → Actions → General → Workflow permissions*: read and write, or package settings → *Manage Actions access* |

## Releases

| Step | What happens |
|---|---|
| 1. Add a `## [X.Y.Z]` section to `CHANGELOG.md` (pull request) | — |
| 2. `git tag -a vX.Y.Z -m X.Y.Z && git push origin vX.Y.Z` | **CI** runs every job on the tag and pushes the image `ghcr.io/francoisgn/gcp-gca-lic-mgmt:X.Y.Z` (no `latest` for a tag) |
| | **Release** workflow creates the GitHub release with the changelog section; a suffix (`-alpha.1`, `-rc.1`) makes it a *pre-release* |

A tag without its changelog section fails the *Release* workflow (the image is still built). Check: *Releases* on the repository page, and *Packages* for the image.

## GitLab CI/CD — keyless delivery to Google Cloud

`.gitlab-ci.yml` runs the same checks as GitHub Actions and delivers the portal from the internal GitLab, with **Workload Identity Federation**: no service account key, anywhere.

```
lint ──┬─ lint:go          gofmt · go vet · staticcheck · govulncheck
       ├─ lint:terraform   fmt · init · validate
       └─ lint:secrets     gitleaks on the history
test ──── test             go test -race on PostgreSQL 16, coverage
build ─── build:image      image → Artifact Registry               default branch, v* tags
deploy ── deploy           scripts/rollout.sh: service + jobs       v* tags (manual on the default branch)
```

### How the pipeline gets Google credentials

```
GitLab job ── id_token (aud = WIF provider) ──▶ Google STS ──▶ federated token
          ──▶ IAM Credentials: generateAccessToken(gcalm-deployer) ──▶ 1 h access token
```

`scripts/ci/gcp-token.sh` does the two exchanges with `curl`. Google accepts the token only if it comes from **the configured GitLab project** and a **protected branch or tag** (attribute condition of the provider, `deploy/terraform/cicd.tf`).

| The deployer can | The deployer cannot |
|---|---|
| 🟢 push images to the `gcalm` Artifact Registry repository | 🔴 change IAM, Cloud SQL, secrets, the scheduler or any other infrastructure |
| 🟢 roll a new image out to `gcalm-web` and the `gcalm-*` jobs | 🔴 read the secrets |

### One-off set-up

| # | Where | What |
|---|---|---|
| 1 | `prod.tfvars` | `gitlab_project_path = "group/project"`, `gitlab_url = "https://gitlab.example.com"`; if Google cannot reach the GitLab instance, `gitlab_jwks_json` = content of `<gitlab_url>/oauth/discovery/keys` |
| 2 | admin workstation | `terraform apply` → `terraform output gitlab_ci_variables` |
| 3 | GitLab → *Settings → CI/CD → Variables* | `GCP_PROJECT_ID`, `GCP_REGION`, `GCP_WIF_PROVIDER`, `GCP_DEPLOYER_SA` (none is secret; mark them *protected*) |
| 4 | GitLab → *Settings → Repository* | protect the default branch and the tags `v*` |
| 5 | runners without Internet | override `GO_IMAGE`, `DOCKER_IMAGE`, `GCLOUD_IMAGE`… with the internal mirror; set `GOPROXY` for the image build |

Docker builds use Docker-in-Docker (privileged runner). Release = push a tag `vX.Y.Z` (changelog section first, as on GitHub).

### Check a delivery

- *Build → Pipelines*: every stage green; `deploy` shows the image rolled out.
- *Cloud Run*: `gcalm-web` → *Revisions* (new revision, 100 % traffic); *Jobs* → `gcalm-*` → the image tag.
- Rollback: re-run the `deploy` job of the previous tag's pipeline, or `scripts/rollout.sh <previous image> <region>`.
