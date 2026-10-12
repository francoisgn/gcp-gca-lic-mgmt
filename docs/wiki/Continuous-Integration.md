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
