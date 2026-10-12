# Functional specification

UI language: **English only**. Every page shows the top bar: logo, navigation (*My license*, *Feedback*; *Admin* and *Observability* for admins), **pool counter** `assigned / total` (Google counters, visible to everyone) and the user.

## 1. Landing page

![Request form](images/landing-request.png)

Layout: **left panel = progress** (steps 1 → 9), **centre = subscription**. The form sections carry the same numbers as the progress panel. The user is identified by Google SSO (IAP).

### Progress steps

| # | Spec ref | Step | Turns green when | Other states |
|---|---|---|---|---|
| 1 | a | Identity | "This is me" clicked (e-mail from SSO) | — |
| 2 | b | Gemini Code Assist training | "Yes, I completed it" | 🔴 "Not yet" → link to the training, process blocked |
| 3 | c | Duration | 7 days · 1 month · 3 months · 6 months · 1 year | — |
| 4 | d | Usage details | every question valid (questions from `policy.yaml`, each answer unlocks the next) | — |
| 5 | W | Directory validation | account OK + eligible GPS code | 🔴 functional KO · 🟠 directory timeout (buffer) |
| 6 | W1 | Admin validation | approved | ⚪ grey when not needed · 🔵 waiting · 🔴 rejected |
| 7 | X | Cloud credentials | IAM bundle granted | 🟠 timeout → buffer |
| 8 | Y | License assignment | license assigned in Google | 🔵 waiting list · 🟠 timeout → buffer |
| 9 | Z | Confirmation e-mail | e-mail sent | 🟠 will be retried |

### States of the centre frame

| State | Shown when | Content |
|---|---|---|
| 🟢 **Active** | Google lists the user in the pool | Since · Until (+ relative) · Last usage · Removal date if unused · reminder of the 7-day rule · VS Code set-up · usage rules (CLI forbidden). All steps green. |
| 🟠 **Buffer** | a pending buffer entry exists | Date of buffering, pending step, retries, **position x / n**, mention *priority assignment*, "come back in ~10 minutes". The failing step is orange on the left. |
| 🔵 **Case 0.1 — pending admin** | escalated request waiting | State, **greyed read-only form** with the previous answers, records counter (*n requests · m rejected*), list of admins (not super admins), previous rejections if any. |
| 🔴 **Case 0.2 — rejected** | last request rejected | Rejections counter, table of rejected requests (10 per page): request date, escalation input, rejection date / admin, reason. Button *"I think this is not legitimate — submit a new request"* reopens the form (pre-filled). |
| 🔵 **Case 1 — waiting list** | request validated, pool exhausted | "Pool exhausted", request date, queue date, **position #x of n**, *Leave the waiting list*. |
| 🔴 **Directory KO** | directory KO after a buffered retry | Reason + *Escalate* / *Withdraw*. |
| **Case 2 — new** | none of the above | If no seat (Google available − reservations ≤ 0): **red box on faded red background** "Pool exhausted — you will join the waiting list at position #X", then the standard process. |

When the user has no active license, a **Previous licenses** table shows each former license (from, to, duration, end reason: *Unused*, *Expired*, *Admin removal*, *Removed outside the portal*, *Subscription ended*).

### Standard process

1. Steps 1 → 4 in the browser (no server call). Server-side validation is repeated on submission.
2. **Step 5** — `POST /api/request/directory`: the request is stored as a draft and the directory is called (5 s).
   - ✅ OK → site, business unit, masked GPS code; the answers are frozen.
   - 🔴 functional KO (account KO, no / invalid GPS code, GPS code not in the allowlist, unknown account, **external account**) → *Withdraw* or *Escalate*. Escalation (**step 6**) asks for division, entity, project and an optional comment, then lists the admins to contact.
   - 🟠 technical KO (timeout, 5xx, network) → **the process stops**: message *"Enterprise directory unavailable — try again in a few minutes"*, nothing is buffered or reserved, the button stays available. The failure is recorded in the technical KPIs.
3. **Steps 7 · 8 · 9** (one form section, badge *7–9*) — `POST /api/request/{id}/submit` with a loading animation; **10 s budget**:
   - seat available → credentials, license, e-mail → 🟢 *Your license is active*;
   - pool exhausted → 🔵 *You are #x in the waiting list* (+ e-mail);
   - Google too slow → 🟠 buffer, *come back in ~10 minutes* (the seat stays reserved).

### Confirmation e-mail (step 9)
Request date, assignment date, planned end · how to set up Gemini Code Assist in VS Code (extension, sign-in, project) · **command-line usage forbidden (Gemini CLI or any terminal / agent tool)** · link to the company AI usage training · reminder of the 7-day rule.

## 2. Automatic rules

| Rule | Detail |
|---|---|
| Auto-approval | User e-mail → directory lookup → GPS code → **GPS code in the allowlist table** (Admin → GPS codes) ⇒ approved. Also required: internal account, training ✓, directory account status `OK`, GPS code format (`policy.eligibility`), business unit allowed (optional list). Otherwise: functional KO, escalation to an administrator possible. |
| External accounts | `firstname.lastname.ext@domain` (suffix `policy.accounts.external_suffix`) → **never auto-approved**: functional KO *"External accounts are never approved automatically"*, escalation possible, an admin decides. |
| Monthly directory re-check | Every active license is re-checked against the directory once a month (assignment anniversary, nightly job). Site / BU / GPS changes → flag **Data changed**; would no longer be auto-approved → flag **No longer eligible**. Nothing is removed automatically. See [[Scheduled Jobs]]. |
| Unused license | No usage for `unused_days` (7) → removed (*Unused*). Last activity = latest of assignment, subscription migration, last usage stored, Google `recentUsageTime` (refreshed by Google within 24 h). |
| Expiry | Requested duration elapsed → removed (*Expired*). |
| Queue | Served by priority (subscription rollover 10 > buffer 5 > normal 0) then FIFO, as soon as a seat is released (event) and every 7 min (job). |
| Buffer | Google timeouts only (credentials, license). Retried every 7 min, before the queue; abandoned after 30 attempts (request FAILED, visible to admins). A directory outage is not buffered: the request stops. |
| Subscription | Refreshed every 3 h (+ button for super admins). Each new subscription makes the previous one inoperative: **as soon as a newer active subscription appears** (or the current one becomes inactive), licenses are migrated to it. If the new pool is smaller, users are kept **by most recent usage** (never-used licenses last); the others go first in the queue (priority) and receive an e-mail with their position. |
| Cloud roles | Removed at every end of license; reconciled with the licenses every 3 h (see [[Google APIs and Permissions]]). |

## 3. Administration (admins and super admins)

![Admin](images/admin-approvals.png)

| Tab | Actions |
|---|---|
| Approvals | Directory answer, escalation details, form answers → **Approve** (assign or queue) / **Reject** (reason mandatory, kept in the user's history, e-mail sent) |
| Waiting list | Position, waiting since, priority → remove a user |
| Buffer | Stage, attempts, last error → **Retry now**; abandoned entries |
| Assign & transfer | Assign a license to an e-mail (source admin) · **Transfer** a license from A to B atomically (A ends with *Admin removal*) |
| GPS codes | Allowlist of GPS codes approved automatically: add one or several codes (comma / space / new line separated, 8 hex characters) with a description, remove a code (existing licenses are kept and flagged by the next directory re-check). Shows the number of active licenses per code. Audited. |
| Notifications | Journal of the e-mails sent (event, recipient, status SENT / FAILED / DISABLED), *View* shows the text. See [[Notifications]]. |
| Decisions & audit | Last decisions and the audit log of every admin / system action |

## 4. Observability (admins)

See [[KPIs and Observability]]. Includes the **inventory** of assigned licenses: filter chips (site, business unit, source, **account: internal / external**, **directory check: data changed / no longer eligible / check failed / never checked**, unused ≥ 7/14/30 days, expiring within 7/30 days), e-mail search, selection, **Remove selected** and **Remove all matching** (mass removal by criteria, refused if the matching count changed since the preview).

Directory column: badge of the last monthly re-check (hover = changed fields, reason or error). Actions on the selection: **Re-check directory** (now, instead of waiting for the anniversary) and **Accept directory changes** (the current directory values become the reference, flag cleared).

Super admins additionally get: **Force subscription refresh**, **Run now** on each job, and (fake mode only) the simulation panel.

## 5. Feedback

Every user can send an improvement idea, a problem or a question. It becomes a Jira (or GitLab) work item created by a technical account; the SSO e-mail of the requester is attached automatically.

## 6. Roles

| Role | Source | Rights |
|---|---|---|
| User | anyone allowed through IAP | own license, feedback |
| Admin | `config/roles.yaml` → `admins` | + admin page, observability, inventory, deviations |
| Super admin | `config/roles.yaml` → `super_admins` | + force refresh, run jobs, simulation (dev) |
