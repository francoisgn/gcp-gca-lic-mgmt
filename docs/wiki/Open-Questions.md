# Open questions and assumptions

Assumptions taken for v1 — to confirm with the business / platform / security owners.

| # | Topic | Assumption in v1 | To confirm |
|---|---|---|---|
| 1 | Step D questions | Placeholder questions (use case, IDE, project, justification) in `config/policy.yaml` | Copy the fields of the current request form into `policy.yaml` (no code change). |
| 2 | Eligibility rule | Auto-approval if account `OK` and GPS code is 8 hex characters; optional allow-lists of GPS codes / business units | Which GPS codes (or rule) are "the right value"? |
| 3 | Directory API | Contract `GET /v1/accounts?email=` with bearer API key (simulated) | Real endpoint, authentication (API key, OAuth, mTLS?) and fields. |
| 4 | Directory technical KO | Buffered like Google timeouts (stage DIRECTORY, no seat reserved) | The spec mentions both "technical KO → buffer" and "buffer only on Google timeouts": kept the former. |
| 5 | Credentials bundle | Google group bound to `cloudaicompanion.user` + `serviceUsageConsumer` | Security validation; group creation and manager rights for the portal service accounts. Option `CREDENTIALS_MODE=none` if roles are granted company-wide (see [[Google APIs and Permissions]]). |
| 6 | Rollover policy | Migration only when the current subscription becomes inactive; oldest licenses kept first when the new pool is smaller | Overlapping subscriptions (renewal before the end)? Keep newest instead? |
| 7 | Purge of never-used licenses | 7 days after assignment (or after a migration) | Same rule for "never used" as for "no longer used"? |
| 8 | Deviations self-healing | A deviation disappearing by itself is closed as `SELF_HEALED` | The spec says it goes back to 0 only by remove / back-port — keep auto-closing? |
| 9 | Feedback tracker | Jira Cloud REST v3 (technical account) — GitLab supported too | Jira project key and issue type, or GitLab project. |
| 10 | E-mail transport | Workspace SMTP relay | Sender address and relay settings. |
| 11 | Escalation notification | Admins see the badge in the top bar; no e-mail to admins | Should admins receive an e-mail per escalation? |
| 12 | Expiry reminder | None | E-mail N days before expiry? (requested in the demo feedback) |
| 13 | Language of the wiki | English, like the UI and the code | — |
| 14 | KPI mock-ups | Built from the written specification (the Excalidraw boards were not reachable from the build environment) | Compare with the boards and adjust. |
