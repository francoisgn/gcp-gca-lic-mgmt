# Scheduled jobs

Cloud Scheduler calls `POST https://gcalm-worker-…run.app/jobs/{name}` with an OIDC token of the `gcalm-scheduler` service account (only principal with `roles/run.invoker` on the worker; the token audience and e-mail are verified again in the app). Each run takes a dedicated PostgreSQL advisory lock (no overlap) and is recorded in `job_runs` (visible in *Observability → Scheduled jobs*). A run in error returns HTTP 500 so Cloud Scheduler retries once and the `gcalm/job_errors` log metric triggers an alert.

| Job | Schedule (time zone `TIMEZONE`) | What it does |
|---|---|---|
| `buffer-retry` | `*/7 * * * *` | Retries each pending buffer entry from its stage (credentials → license — Google timeouts only, a directory outage is never buffered); re-sends failed confirmation e-mails; abandons an entry after 30 attempts. |
| `queue` | `3-59/7 * * * *` | Serves the waiting list while seats are available (max 50 per run). Also triggered immediately after any release of seats (admin removal, purge, deviation fix). |
| `subscription-refresh` | `0 */3 * * *` | Lists the orders, updates the subscriptions, **switches to the newest active subscription and migrates the licenses** (most recent users first when the new pool is smaller; the others are queued with priority and told their position), reconciles with Google (deviations, last usage), **reconciles the IAM bundle group with the licenses** (removes roles left without a license, restores missing ones), stores daily seat counters. Super admins can run it from the UI (*Force subscription refresh*). |
| `purge` | `15 * * * *` | Removes expired licenses and licenses unused for 7 days, e-mails the users, serves the queue. |
| `directory-recheck` | `30 2 * * *` | Monthly re-check of active licenses against the enterprise directory (see below). |
| `housekeeping` | `30 3 * * *` | Deletes technical KPIs older than 10 days, job history older than 30 days and the e-mail journal older than 90 days. |

Locally (`INPROCESS_SCHEDULER=true`) the same jobs run on tickers inside the process.

## Monthly directory re-check

Goal: a license granted on a directory answer stays justified. Each active license is checked **once a month, on the anniversary day of its assignment** — so the load is spread over the month (~1/30 of the licenses per night).

| Rule | Detail |
|---|---|
| Due tonight | day of month of `assigned_at` = today (licenses assigned on the 29th–31st are checked on the last day of shorter months) |
| Catch-up | no successful check for 35 days (directory errors, missed nights) |
| Guards | never during the first 20 days of a license, never twice within 20 days |
| Volume | `policy.directory_recheck.max_per_run` (2000) per night, `concurrency` (4) parallel calls |

| Result | Meaning | Inventory |
|---|---|---|
| `OK` | same site / business unit / GPS code, still eligible | badge *ok* |
| `CHANGED` | site, business unit or GPS code differ from the values recorded at assignment | badge *changed* (fields in the tooltip), filter *Data changed* |
| `INELIGIBLE` | would not be auto-approved today: account KO or not found, external, GPS code missing / not in the allowlist, BU not allowed | badge *not eligible* (reason in the tooltip), filter *No longer eligible* |
| `ERROR` | directory timeout / 5xx — retried the next night (catch-up rule) | badge *check failed* |

**Nothing is removed automatically.** Administrators filter the inventory, then *Accept directory changes* (new values become the reference), *Re-check directory* on demand, or remove the licenses (mass removal). Licenses assigned by an admin without directory data adopt the first answer as their reference.

Disable: `policy.directory_recheck.enabled: false`.
