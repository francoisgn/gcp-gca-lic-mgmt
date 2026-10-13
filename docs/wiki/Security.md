# Security

| Topic | Measure |
|---|---|
| Authentication | Identity-Aware Proxy (Google SSO). The app validates the signed `X-Goog-IAP-JWT-Assertion` (ES256, audience = the Cloud Run service) and never trusts plain identity headers. `AUTH_MODE=dev` is refused when `APP_ENV=prod`. |
| Authorization | Roles from `config/roles.yaml` (git, PR-reviewed). Checked server-side on every page and API call. Users can only act on their own requests. |
| Jobs endpoint | Separate Cloud Run service, IAM-protected (`run.invoker` only for the scheduler SA) + OIDC audience / e-mail re-verified in the app. |
| CSRF | Go `http.CrossOriginProtection` (Sec-Fetch-Site / Origin) on every state-changing request; JSON bodies only. |
| XSS | `html/template` auto-escaping; JS builds DOM with `textContent` only; strict CSP: `script-src 'self'`, no inline script, `frame-ancestors 'none'`. |
| Abuse / DoS | Per-user token bucket (HTTP 429), shared Google pool snapshot (singleflight + TTL), bounded request bodies (64 KiB), server timeouts. |
| Secrets | Secret Manager → env vars; nothing secret in git or in the image. Versioned settings only hold the **name** of the variable (`*_secret_env`). |
| CI/CD | Keyless: GitLab pipelines get a 1-hour token of `gcalm-deployer` through Workload Identity Federation, only for one project and protected refs. The deployer can push images and roll them out, nothing else. |
| Supply chain | CI on every push: `govulncheck` (known vulnerabilities reachable from the code), `staticcheck`, **gitleaks** on the whole history; Dependabot weekly updates (Go, Actions, Docker, Terraform); tools and actions pinned by version. Public repository: enable *Secret scanning* + *Push protection* and *Private vulnerability reporting* (see `SECURITY.md`). |
| Least privilege | Custom role limited to license operations on the billing account; IAM bundle only for licensed users; distroless non-root container. |
| Data | Minimal personal data (e-mail, directory site / BU / GPS code, form answers). GPS code masked in the user UI. Cloud SQL encrypted at rest, TLS-only, PITR backups. Retention: technical KPIs 10 days, job history 30 days, e-mail journal 90 days. |
| External accounts | `*.ext@` accounts are never approved automatically; an administrator decides; inventory filter and KPI. |
| Audit | `audit_log` for every admin action (approve, reject, assign, transfer, remove, mass remove, deviation resolution, GPS codes added / removed, directory changes accepted, simulation), plus the request history and the e-mail journal. |
| Dependencies | Go standard library + pgx, yaml, x/time, x/sync and Google official clients; Chart.js vendored (MIT). |
