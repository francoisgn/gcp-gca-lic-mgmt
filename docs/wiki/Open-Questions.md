# Open questions and assumptions

Assumptions taken in the current version — to confirm with the business / platform / security owners.

## Open

| # | Topic | Assumption | To confirm |
|---|---|---|---|
| 1 | Step D questions | Placeholder questions (use case, IDE, project, justification) in `config/policy.yaml` | Copy the fields of the current request form into `policy.yaml` (no code change). |
| 3 | Directory API | Configurable contract in `config/settings.yaml` (path, query parameter, bearer / header key, field mapping) | Real endpoint, authentication (API key? OAuth, mTLS → code change) and field names. |
| 4 | Directory technical KO | Buffered like Google timeouts (stage DIRECTORY, no seat reserved) | The spec mentions both "technical KO → buffer" and "buffer only on Google timeouts": kept the former. |
| 5 | Credentials bundle | Google group bound to `cloudaicompanion.user` + `serviceUsageConsumer` | Security validation; group creation and manager rights for the portal service accounts. Option `CREDENTIALS_MODE=none` if roles are granted company-wide (see [[Google APIs and Permissions]]). |
| 6 | Rollover policy | Migration only when the current subscription becomes inactive; oldest licenses kept first when the new pool is smaller | Overlapping subscriptions (renewal before the end)? Keep newest instead? |
| 7 | Purge of never-used licenses | 7 days after assignment (or after a migration) | Same rule for "never used" as for "no longer used"? |
| 8 | Deviations self-healing | A deviation disappearing by itself is closed as `SELF_HEALED` | The spec says it goes back to 0 only by remove / back-port — keep auto-closing? |
| 10 | E-mail transport and templates | Workspace SMTP relay; **mock-up templates** | Sender address, relay settings; corporate templates (same file names and fields). |
| 12 | Expiry reminder | None | E-mail N days before expiry? |
| 15 | External accounts | Identified by the local part suffix `.ext` (`firstname.lastname.ext@`) | Does the directory also flag externals (field to use instead of / on top of the suffix)? |
| 16 | Directory re-check outcome | Flags only (*changed*, *no longer eligible*); admins decide | Should a *no longer eligible* license be removed automatically after N days? Notify the user? |
| 17 | Drift reference | Site, business unit and GPS code compared with the values at assignment | Other fields to watch (manager, contract end date…)? |
| 18 | Re-check of admin-assigned licenses | Same rules; the first answer becomes the reference when the assignment had no directory data | Should admin-approved exceptions (e.g. externals) be excluded from *no longer eligible*? |

## Resolved

| # | Topic | Decision |
|---|---|---|
| 2 | Eligibility rule | Account `OK` + GPS code format + **GPS code in the allowlist table managed by admins** (*Admin → GPS codes*); externals never auto-approved. |
| 9 | Feedback tracker | GitLab, configured in `config/settings.yaml` (token in Secret Manager). Jira still supported. |
| 11 | Escalation notification | Admins (not super admins) receive an e-mail per escalation; the user receives a confirmation. See [[Notifications]]. |
| 13 | Language of the wiki | English, like the UI and the code. |
| 14 | KPI mock-ups | Built from the written specification; the Excalidraw boards can be compared by exporting them (PNG / SVG) — the build environment cannot read them. |
| 19 | Approval e-mails | One e-mail when the license is active right away, two (approved, then active) when the user has to wait. |
