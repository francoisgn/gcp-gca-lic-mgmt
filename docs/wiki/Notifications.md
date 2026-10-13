# E-mail notifications

One e-mail per change of step, to the user concerned (and to the administrators for a new escalation). Templates: `internal/mail/templates/*.html|.txt` — **mock-ups** to be replaced by the corporate versions (same file names, same data fields).

## Event matrix

| Event (`policy.yaml` key) | Sent to | When | Template |
|---|---|---|---|
| `request_escalated` | user | the user escalates a directory KO (step 6) — lists the admins to contact | `escalated` |
| `admin_new_escalation` | every **admin** (super admins excluded) | same moment — requester, reason, division / entity / project / comment, *external* tag | `admin_escalation` |
| `request_rejected` | user | an admin rejects — reason, link to submit a new request | `rejected` |
| `request_approved` | user | an admin approves **but the license is not active yet** (pool exhausted → queue position, or Google timeout → buffer) | `approved` |
| `license_assigned` | user | license active = **end of the process** (step 9): dates, VS Code set-up, CLI forbidden, AI training; mentions the approving admin when there was one | `assigned` |
| `queued` | user | pool exhausted on a normal request — position in the waiting list | `queued` |
| `buffered` | user | a Google service timed out (credentials or license) — pending step, automatic retry every 7 min | `buffered` |
| `removed_unused` | user | removed after 7 days without usage | `removed` |
| `removed_expired` | user | requested duration elapsed | `removed` |
| `removed_admin` | user | removed by an admin (single, mass removal, or source of a transfer) | `removed` |
| `removed_subscription` | user | not kept in the new, smaller subscription (seats go to the most recent users) — back in the waiting list with priority, **with their position #x** | `removed` (*requeued* variant) |

Not notified: a license removed outside the portal (Google console) — it is a deviation for the admins, not a user event. A directory outage is not notified either: the user sees the message on screen, nothing was sent.

## Admin approval: one or two e-mails?

Decision: **one e-mail when the license is active right away, two otherwise.**

| After approval | E-mails | Why |
|---|---|---|
| Seat available, Google OK (usual case) | 1 × `license_assigned` *"approved by …, your license is active"* | the approval and the result happen in the same second: a separate "approved" e-mail would be noise |
| Pool exhausted | `request_approved` (with queue position) → later `license_assigned` | the user must know the decision now; the license may come days later |
| Google timeout | `request_approved` → `license_assigned` a few minutes later | same |

## Journal

Every attempt is stored in the `notifications` table — `SENT`, `FAILED` (with the error) or `DISABLED` (event switched off in `policy.yaml`) — with the text version of the e-mail. Visible in **Admin → Notifications** (last 100, *View* opens the text). Kept 90 days (housekeeping job).

Sending is best effort and never blocks a user action. The confirmation e-mail of step 9 is the exception: if it fails, the buffer job retries it until it is sent.

## Switching an event off

```yaml
# config/policy.yaml
notifications:
  queued: false        # no e-mail when joining the waiting list
```
A missing key means *on*. Pull request + redeploy.
