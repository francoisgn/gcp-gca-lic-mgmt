# Data model

PostgreSQL 16, migrations embedded in the binary (`internal/store/migrations`, applied at start-up under an advisory lock). E-mails are stored lower-cased; timestamps are `TIMESTAMPTZ`.

```
subscriptions 1──* license_assignments *──1 license_requests 1──* queue_entries
                                                       └────────1──* buffer_entries
deviations (user, subscription, kind)     api_calls (10 days)     daily_stats (per day)
feedback     audit_log     job_runs     schema_migrations
```

| Table | Purpose | Key invariants |
|---|---|---|
| `subscriptions` | Gemini Code Assist orders seen in Google (state, window, seats) | one `is_current` row |
| `license_requests` | one row per form submission / admin assignment / rollover re-queue: answers, directory result, escalation, decision, fulfilment timestamps, **seat reservation** (`reserved_at`) | one open request per user |
| `license_assignments` | licenses granted by the portal: source (`AUTO`, `ADMIN`, `RECONCILED`), duration, expiry, last usage, directory attributes (site, BU, GPS) for filters, end reason | one active assignment per user |
| `queue_entries` | waiting list (priority, enqueue time, outcome) | one waiting entry per user |
| `buffer_entries` | timed-out operations (stage, attempts, last error) | one pending entry per user |
| `deviations` | differences with Google (`GOOGLE_ONLY`, `DB_ONLY`) and their resolution | one open deviation per (user, subscription, kind) |
| `api_calls` | every outbound call: target, operation, context (UX / JOB / ADMIN), duration, outcome | purged after 10 days |
| `daily_stats` | page views per day, seats total / assigned per day | — |
| `feedback` | user feedback and tracker key | — |
| `audit_log` | admin and system actions | — |
| `job_runs` | scheduled job executions | purged after 30 days |

History-based KPIs (flows, queue / buffer / deviation depth per day) are **recomputed from timestamps** (`assigned_at`, `ended_at`, `enqueued_at`, `left_at`, `created_at`, `resolved_at`, `detected_at`), so a missed job never creates a hole in the charts.
