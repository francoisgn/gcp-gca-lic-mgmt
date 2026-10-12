# Configuration

Environment variables (12-factor) + two YAML files versioned in git. Secrets come from Secret Manager as environment variables.

## YAML files

| File | Content | Change process |
|---|---|---|
| `config/roles.yaml` | `super_admins`, `admins` (e-mails) | pull request + redeploy |
| `config/policy.yaml` | durations, default duration, `unused_days` (7), `buffer_max_attempts` (30), eligibility rules (account statuses, GPS code pattern, optional GPS / BU allow-lists), questions of step D (`select` / `text` / `textarea`, required, min / max length), links (training, AI training, VS Code guide, usage policy, support) | pull request + redeploy |

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `APP_MODE` | `all` | `web` (portal), `worker` (jobs), `all` (dev) |
| `APP_ENV` | `dev` | `prod` forbids dev authentication and demo data |
| `PORT` | `8080` | |
| `BASE_URL` | `http://localhost:8080` | portal URL used in e-mails |
| `TIMEZONE` | `Europe/Paris` | dates and per-day KPIs |
| `DATABASE_URL` | local | PostgreSQL DSN (Cloud Run: unix socket `/cloudsql/...`) — **secret** |
| `AUTH_MODE` | `dev` | `iap` in production |
| `IAP_AUDIENCE` | | `/projects/NUMBER/locations/REGION/services/gcalm-web` |
| `DEV_DEFAULT_USER` | `jane.doe@example.com` | identity when no dev cookie |
| `ROLES_FILE` / `POLICY_FILE` | `config/…` | |
| `GOOGLE_MODE` | `fake` | `real` = Procurement API |
| `BILLING_ACCOUNT_ID` | | billing account holding the orders |
| `QUOTA_PROJECT` | | project billed for API quota |
| `GCA_ORDER_MATCH` | `(?i)gemini\|code ?assist\|cloudaicompanion` | regex identifying GCA orders (offer + display name) |
| `GCA_ORDER_IDS` | | explicit comma-separated order IDs (bypass the regex) |
| `GCA_PROJECT_ID` | | project users select in the IDE (e-mail) |
| `CREDENTIALS_MODE` | `fake` | `group` (Cloud Identity), `none` (roles granted elsewhere) |
| `CREDENTIALS_GROUP` | | e-mail of the IAM bundle group |
| `DIRECTORY_MODE` | `simulated` | `http` = real directory |
| `DIRECTORY_URL` / `DIRECTORY_API_KEY` | | **API key secret** |
| `DIRECTORY_TIMEOUT` | `5s` | |
| `MAIL_MODE` | `log` | `smtp` |
| `SMTP_HOST` / `SMTP_PORT` / `SMTP_USER` / `SMTP_PASSWORD` / `MAIL_FROM` | `smtp-relay.gmail.com` / 587 | **password secret** |
| `TRACKER_MODE` | `log` | `jira` or `gitlab` |
| `JIRA_URL` / `JIRA_USER` / `JIRA_TOKEN` / `JIRA_PROJECT` / `JIRA_ISSUE_TYPE` | `Task` | **token secret** |
| `GITLAB_URL` / `GITLAB_TOKEN` / `GITLAB_PROJECT` | `https://gitlab.com` | **token secret** |
| `UX_TIMEOUT` | `10s` | budget of steps X+Y when the user waits |
| `JOB_CALL_TIMEOUT` | `30s` | per call in jobs and admin actions |
| `POOL_CACHE_TTL` | `60s` | Google pool snapshot cache |
| `RATE_LIMIT_PER_MIN` / `RATE_LIMIT_BURST` | `120` / `30` | per user |
| `METRICS_RETENTION` | `240h` | technical KPIs retention |
| `BUFFER_RETRY_EVERY` | `7m` | displayed to users (the real schedule is Cloud Scheduler's) |
| `INPROCESS_SCHEDULER` | `false` | run jobs on tickers (dev) |
| `JOBS_AUDIENCE` / `JOBS_INVOKER` | | re-verify scheduler OIDC tokens |
| `LOG_LEVEL` | `info` | `debug` logs every outbound call |
| Dev only: `DEMO_SEED`, `FAKE_GOOGLE_STATE_FILE`, `FAKE_GOOGLE_SEATS`, `FAKE_GOOGLE_MAX_LATENCY`, `FAKE_GOOGLE_HANG_RATE`, `DIRECTORY_SIM_FAILURE_RATE`, `DIRECTORY_SIM_MAX_LATENCY` | | simulators |

Invalid combinations stop the process at start-up with an explicit message.
