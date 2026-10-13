# Runbook

| Symptom | Diagnosis | Action |
|---|---|---|
| 🔴 Top bar shows `— / —` "pool unavailable" | Google Procurement API unreachable or permission missing (`api_calls` outcome `HTTP_4XX` → permissions, `TIMEOUT`/`5XX` → Google side) | Check the custom role on the billing account and the quota project. The landing page degrades gracefully (database view + warning). |
| 🔴 *Inactive — no replacement found* on the subscription card | The current order ended and no new active order matches `GCA_ORDER_MATCH` / `GCA_ORDER_IDS` | Check the new order in the console; adjust the regex or the explicit IDs; *Force subscription refresh*. Licenses are migrated as soon as the new order is seen. |
| 🟠 Buffer growing | Timeouts towards Google (*Technical KPIs → Timeouts*) | Usually transient: the job retries every 7 min. If it lasts, check the Google status. *Retry now* per entry from *Admin → Buffer*. |
| 🟠 Users see *"Enterprise directory unavailable"* | Directory down or slow (*Technical KPIs → Enterprise directory — load & availability*, *errors by type*) | Nothing is lost: users retry. Contact the directory owner; if the drop happens during the monthly re-check (02:30), lower `policy.directory_recheck.concurrency`. |
| 🟠 `iam_revoked` > 0 in the *subscription-refresh* summary | Members of the IAM bundle group without a license (failed revocation, member added by hand) were removed | Expected occasionally. Many at once: someone edits the group by hand — the group must be managed by the portal only. Details in the audit log (`credentials.reconcile`). |
| 🔴 Entries in *Abandoned after too many retries* | 30 failed attempts; the request is FAILED, its seat released | Fix the cause, then ask the user to submit again (or *Assign* from the admin page). |
| 🟠 Requests FAILED with `CREDENTIALS: HTTP 403` | The portal service account is not manager of the IAM bundle group | Add it as manager ([[Authorizing the Portal]]), then users resubmit. |
| 🔴 Deviations | GOOGLE_ONLY: license assigned outside the portal (previous tool, console); DB_ONLY: license removed outside the portal | Resolve from *Observability → Deviations*: *Back-port* (or *Back-port all*) adopts Google-only licenses after a directory check. Avoid manual console changes: they bypass the rules. |
| 🟠 *Back-port all* reports *pending* | The enterprise directory did not answer for those users | Nothing was back-ported for them; click again later. |
| Parallel run with the previous tool | — | Google is the truth: licenses unknown to the portal only show as *Google only* deviations (chart *Open deviations per day*); the portal never touches them until they are back-ported. Back-port when ready (per line or *Back-port all*), then filter the inventory on *Source → back-ported* + *Directory check → No longer eligible* to review the flagged ones. |
| 🔴 Job failing (alert `gcalm/job_errors`) | *Observability → Scheduled jobs* shows the error; *Cloud Run → Jobs → gcalm-<name> → Executions* for the logs | Fix and *Run now* (or *Execute* the Cloud Run job). Jobs are idempotent (safe to re-run). |
| 🔴 Bad release | New image misbehaves | Roll the previous image back: re-run the `deploy` job of the previous tag, or `scripts/rollout.sh <previous image> <region>`. |
| 🟠 Users complain about HTTP 429 | Rate limit per user | Raise `RATE_LIMIT_PER_MIN` / `RATE_LIMIT_BURST` if legitimate. |
| Need to free seats quickly | — | *Observability → Inventory*: chip *Unused for ≥ 7 days* (or 14 / 30) → *Remove all matching*. Released seats go to the waiting list automatically. |
| A business unit asks for its consumption | — | *Observability → Consumption* (by business unit or GPS code) → click its line: the inventory shows its licenses, page by page. |
| Give a license to a specific person when the pool is full | — | *Admin → Assign & transfer → Transfer* from a holder to the person (atomic; the queue cannot take the seat). |
| Add / remove an admin | — | Pull request on `config/roles.yaml`, merge, deploy. |

## Useful SQL (read-only)

```sql
-- requests stuck in flight (reservation older than 15 min, ignored by the seat accounting)
SELECT id, user_email, status, reserved_at FROM license_requests
 WHERE reserved_at < now() - interval '15 minutes' AND license_at IS NULL AND status <> 'BUFFERED';

-- licenses expiring this week
SELECT user_email, expires_at FROM license_assignments WHERE ended_at IS NULL AND expires_at < now() + interval '7 days';

-- outbound errors of the last hour
SELECT target, operation, outcome, count(*) FROM api_calls WHERE ts > now() - interval '1 hour' AND outcome <> 'OK' GROUP BY 1,2,3;
```
