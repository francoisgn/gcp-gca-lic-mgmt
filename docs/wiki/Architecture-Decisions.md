# Architecture decisions (ADR)

Short records of the structuring choices. Status: ✅ accepted · 🟠 to confirm with the platform / security teams.

## ADR-01 — Go, server-rendered UI, no front-end build ✅
- **Context**: small team, a handful of screens, long-lived internal tool.
- **Decision**: Go 1.26, `html/template`, vanilla JS, Chart.js vendored; a single static binary embedding templates and assets.
- **Consequences**: no npm supply chain in production, trivial container (distroless), strict CSP possible (no inline script). Rich client interactions are limited to what vanilla JS does comfortably — enough for a progressive form, tables and charts.

## ADR-02 — Cloud Run, two services from one image ✅
- **Decision**: `gcalm-web` (behind IAP) and `gcalm-worker` (jobs) run the same image with `APP_MODE=web|worker`.
- **Why two**: Cloud Scheduler must authenticate with an OIDC token checked by Cloud Run IAM, while users go through IAP. Separating them keeps the job endpoints unreachable from users, and lets jobs run long (15 min timeout) without affecting the portal.
- **Alternative rejected**: Cloud Run *jobs* — one container start per run every 7 minutes is slower and costlier than an idle-to-zero service; GKE — not justified.

## ADR-03 — Cloud SQL for PostgreSQL ✅
- **Decision**: PostgreSQL 16 (Enterprise edition, 1 vCPU, PITR backups).
- **Why**: the domain is relational (requests ↔ assignments ↔ queue ↔ buffer), KPIs are SQL aggregations per day, invariants are enforced with partial unique indexes, seat consumption is serialised with advisory locks.
- **Alternative rejected**: Firestore (no window functions for queue positions, no cheap per-day aggregations, invariants in code only).

## ADR-04 — Identity-Aware Proxy for SSO ✅
- **Decision**: IAP enabled directly on Cloud Run; the app validates the `X-Goog-IAP-JWT-Assertion` signature and audience and uses the `email` claim.
- **Why**: Google SSO "for free", device / context-aware policies available, no OAuth client to maintain.

## ADR-05 — Google is the source of truth; the database is a working copy ✅
- **Decision**: the user's license status shown on the landing page comes from Google (pool enumeration). The database stores the *business context* (why, until when, history). A reconciliation every 3 h records **deviations** (GOOGLE_ONLY / DB_ONLY) that stay open until resolved by an admin (remove / back-port / close / re-assign) or until they disappear by themselves (`SELF_HEALED`).

## ADR-06 — Protect Google from refresh storms ✅
- **Decision**: one pool snapshot (getPool + enumerate) shared by all users through *singleflight* + 60 s TTL, plus a per-user token bucket (120 req/min, burst 30, HTTP 429 beyond).
- **Effect**: at most ~1 enumeration per minute whatever the traffic. The "app access vs Google checks" chart makes the ratio visible.

## ADR-07 — Seat reservation instead of a long lock ✅
- **Context**: a first version held the pool lock during the Google calls; a hanging call blocked every other user beyond the 10 s UX budget (found by an end-to-end test).
- **Decision**: lock only to *reserve* (read counter − reservations, write `reserved_at`), then call Google without lock. Reservations count against availability: buffered requests keep their seat (priority over the queue), stale in-flight reservations expire after 15 min.

## ADR-08 — IAM bundle through a Google group 🟠
- **Decision**: grant `roles/cloudaicompanion.user` + `roles/serviceusage.serviceUsageConsumer` once to a group on the Gemini project; credentials = group membership (Cloud Identity API).
- **Why not per-user IAM bindings**: asynchronous, 1,500-principal policy limit, concurrent policy edits.
- **Why not everyone in the company**: see [[Google APIs and Permissions#why-not-grant-the-roles-to-everyone]].
- **Lifecycle**: removed at every end of license; reconciled with the licenses every 3 hours (stray members removed, missing ones restored — Google part of the reference, owners / managers never touched), so the roles never accumulate.
- **To confirm**: the portal service accounts must be *managers* of the group ([[Authorizing the Portal]]).

## ADR-09 — Manual assignment protocol on the license pool ✅
- The pool must stay in **manual** assignment mode: the portal is the only assignment authority (automatic assignment would bypass training and directory checks, and would make individual assign / unassign impossible).

## ADR-10 — Buffer only for transient failures ✅
- Google timeouts, 5xx, 429 and network errors (credentials, license) → buffer (retried every 7 min, abandoned after 30 attempts). Permanent errors (4xx) → request FAILED with the reason, visible to admins.
- **A directory outage is not buffered** (0.1.0-alpha.1): the request stops with *"Enterprise directory unavailable"* and the user tries again — a validation that cannot be performed must not leave a pending request; the failure is measured in the technical KPIs (availability).

## ADR-11 — Admins as code ✅
- `config/roles.yaml` in git: pull request = four-eyes review, git log = audit trail. No UI to grant roles.

## ADR-12 — Feedback through a technical tracker account ✅
- Users do not all have a Jira / GitLab account: issues are created by a technical account (token in Secret Manager); the requester's SSO e-mail is written in the issue body and kept in the `feedback` table.

## ADR-13 — The newest subscription wins; most recent users keep their seat ✅
- Each new Gemini Code Assist subscription makes the previous one inoperative, even when Google still shows it active. The refresh switches to the **newest active subscription** (latest start, then largest pool) as soon as it appears and migrates every license.
- New pool smaller: seats go by **most recent usage** (database or Google `recentUsageTime`, the freshest), never-used licenses last. The others go first in the waiting list (priority 10) and receive an e-mail with their position.

## ADR-14 — No account "with rights": the Cloud Run service accounts ✅
- The portal acts with the identities of its Cloud Run services (ADC, short-lived tokens): no personal account, no key file to store or rotate. Least privilege: a custom role on the billing account (license pool only, no purchase / cancellation) and manager of one group. See [[Authorizing the Portal]].
