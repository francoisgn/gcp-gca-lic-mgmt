# Open questions and assumptions

Assumptions taken in the current version — to confirm with the business / platform / security owners.

## Open

| # | Topic | Assumption | To confirm |
|---|---|---|---|
| 1 | Step D questions | Placeholder questions (use case, IDE, project, justification) in `config/policy.yaml` | 🟠 The product owner copies the fields of the current request form into `policy.yaml` (no code change). |
| 3 | Directory API | Configurable contract in `config/settings.yaml` (path, query parameter, bearer / header key, field mapping) | Real endpoint, authentication (API key? OAuth, mTLS → code change) and field names. |
| 7 | Purge of never-used licenses | 7 days after assignment (or after a migration) | Same rule for "never used" as for "no longer used"? |
| 10 | E-mail transport and templates | Workspace SMTP relay; **mock-up templates** | Sender address, relay settings; corporate templates (same file names and fields). |
| 12 | Expiry reminder | None | E-mail N days before expiry? |
| 15 | External accounts | Identified by the local part suffix `.ext` (`firstname.lastname.ext@`) | Does the directory also flag externals (field to use instead of / on top of the suffix)? |
| 16 | Directory re-check outcome | Flags only (*changed*, *no longer eligible*); admins decide | Should a *no longer eligible* license be removed automatically after N days? Notify the user? |
| 17 | Drift reference | Site, business unit and GPS code compared with the values at assignment | Other fields to watch (manager, contract end date…)? |
| 18 | Re-check of admin-approved exceptions | Same rules: an external approved by an admin shows *no longer eligible* every month | Exclude admin-approved exceptions from *no longer eligible*? |
| 20 | Command-line usage | Forbidden by policy (e-mail, training) | Cannot be enforced by IAM: the same license and role allow the Gemini CLI. Accept as a usage rule, or monitor? |

## Resolved

| # | Topic | Decision |
|---|---|---|
| 2 | Eligibility rule | User e-mail → directory lookup → GPS code → **GPS code in the allowlist table** (managed by admins in *Admin → GPS codes*) ⇒ automatic approval. Otherwise rejected, escalation to an administrator possible. Externals never auto-approved. |
| 4 | Directory technical KO | **The process stops** with *"Enterprise directory unavailable"*; nothing is buffered, the user tries again. Failures are measured in the technical KPIs — *Enterprise directory — load & availability* shows user requests and the monthly re-check side by side, to see whether the directory holds the load. |
| 5 | Credentials bundle | Group bound to the two roles — not granted to everyone. Roles removed at every end of license and **reconciled every 3 hours** with the licenses, so they never accumulate. How to authorize the portal: [[Authorizing the Portal]]. |
| 6 | Renewal policy | Each new subscription makes the previous one inoperative: the **newest active subscription wins** as soon as it appears. Smaller pool: users kept **by most recent usage**; the others are queued first and told their position (#x) by e-mail. |
| 8 | Deviations self-healing | A deviation that disappears by itself (e.g. the console change was reverted) is closed as `SELF_HEALED` and leaves the dashboard. |
| 9 | Feedback tracker | GitLab, configured in `config/settings.yaml` (token in Secret Manager). Jira still supported. |
| 11 | Escalation notification | Admins (not super admins) receive an e-mail per escalation; the user receives a confirmation. See [[Notifications]]. |
| 13 | Language of the wiki | English, like the UI and the code. |
| 14 | KPI mock-ups | Built from the written specification; the Excalidraw boards can be compared by exporting them (PNG / SVG) — the build environment cannot read them. |
| 19 | Approval e-mails | One e-mail when the license is active right away, two (approved, then active) when the user has to wait. |
