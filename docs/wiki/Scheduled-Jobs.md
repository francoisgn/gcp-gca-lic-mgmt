# Scheduled jobs

Cloud Scheduler calls `POST https://gcalm-worker-…run.app/jobs/{name}` with an OIDC token of the `gcalm-scheduler` service account (only principal with `roles/run.invoker` on the worker; the token audience and e-mail are verified again in the app). Each run takes a dedicated PostgreSQL advisory lock (no overlap) and is recorded in `job_runs` (visible in *Observability → Scheduled jobs*). A run in error returns HTTP 500 so Cloud Scheduler retries once and the `gcalm/job_errors` log metric triggers an alert.

| Job | Schedule (time zone `TIMEZONE`) | What it does |
|---|---|---|
| `buffer-retry` | `*/7 * * * *` | Retries each pending buffer entry from its stage (directory → credentials → license); re-sends failed confirmation e-mails; abandons an entry after 30 attempts. |
| `queue` | `3-59/7 * * * *` | Serves the waiting list while seats are available (max 50 per run). Also triggered immediately after any release of seats (admin removal, purge, deviation fix). |
| `subscription-refresh` | `0 */3 * * *` | Lists the orders, updates the subscriptions, **migrates licenses when the current subscription becomes inactive and a new one is active**, reconciles with Google (deviations, last usage), stores daily seat counters. Super admins can run it from the UI (*Force subscription refresh*). |
| `purge` | `15 * * * *` | Removes expired licenses and licenses unused for 7 days, e-mails the users, serves the queue. |
| `housekeeping` | `30 3 * * *` | Deletes technical KPIs older than 10 days and job history older than 30 days. |

Locally (`INPROCESS_SCHEDULER=true`) the same jobs run on tickers inside the process.
