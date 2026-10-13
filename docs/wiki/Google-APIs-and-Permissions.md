# Google APIs and permissions

## License management — Cloud Commerce Consumer Procurement API

Service `cloudcommerceconsumerprocurement.googleapis.com`, used through the official Go client (`cloud.google.com/go/commerce/consumer/procurement/apiv1`, REST transport).

| Operation | REST | Used by |
|---|---|---|
| List orders (subscriptions) | `GET /v1/billingAccounts/{ba}/orders` | subscription refresh |
| Get the license pool | `GET /v1/billingAccounts/{ba}/orders/{order}/licensePool` | seat check, counters |
| Assign | `POST …/licensePool:assign` `{"usernames": [...]}` | fulfilment, migration |
| Unassign | `POST …/licensePool:unassign` `{"usernames": [...]}` | purge, admin removal |
| Enumerate licensed users | `GET …/licensePool:enumerateLicensedUsers` | landing status (cached), purge, reconciliation |

Useful fields: `LicensePool.totalLicenseCount`, `availableLicenseCount`, `licenseAssignmentProtocol` (must be **manual**); `LicensedUser.username`, `assignTime`, `recentUsageTime` (*"may not be the most recent usage time, updated within 24 hours"*); `Order.lineItems[].lineItemInfo.subscription.{startTime,endTime,autoRenewalEnabled}`.

A subscription is considered **active** when it has a non-cancelled line item whose subscription window covers *now* and its pool has seats. Gemini Code Assist orders are recognised with `GCA_ORDER_MATCH` (regex on offer + display name) or listed explicitly with `GCA_ORDER_IDS`.

**Permissions** — custom organization role `gcaLicensePortal` granted on the **billing account** to the web and worker service accounts (step by step, with and without Terraform: [[Authorizing the Portal]]):

```
billing.accounts.get
consumerprocurement.orders.get
consumerprocurement.orders.list
consumerprocurement.licensePools.get
consumerprocurement.licensePools.assign
consumerprocurement.licensePools.unassign
consumerprocurement.licensePools.enumerateLicensedUsers
```

The service accounts also need `serviceusage.services.use` on the quota project (`QUOTA_PROJECT`, sent as `X-Goog-User-Project`).

## Credentials needed by users — the IAM bundle

To use Gemini Code Assist Standard / Enterprise a user needs **a license and two IAM roles** on the project selected in the IDE (where `cloudaicompanion.googleapis.com` is enabled):

| Role | Title |
|---|---|
| `roles/cloudaicompanion.user` | Gemini for Google Cloud User |
| `roles/serviceusage.serviceUsageConsumer` | Service Usage Consumer |

**Bundle** (implemented): both roles are bound once (Terraform) to the group `CREDENTIALS_GROUP`. The portal grants credentials by adding the user to the group and revokes them when the license ends (Cloud Identity Groups API, `memberships.create` / `lookup` / `delete`). The portal service accounts must be **managers** of the group (Terraform does it when it creates the group; otherwise add them by hand).

Compared with the previous asynchronous per-user grants: one synchronous call (~200–500 ms), no IAM policy edit, no 1,500-member policy limit, revocation symmetrical with the license.

### When are the roles removed?

**Every time a license ends**, the user leaves the group in the same operation:

| End of license | Roles removed by |
|---|---|
| 7 days without usage · requested duration elapsed | `purge` job |
| Admin removal · transfer (source user) · mass removal | the admin action |
| Not kept at a subscription renewal (back in the queue) | `subscription-refresh` migration |
| Deviation resolved — *Remove from Google* (GOOGLE_ONLY) or *Close record* (DB_ONLY, license already gone in Google) | the admin action |

**Safety net — reconciliation every 3 hours** (`subscription-refresh`): a removal is a network call and can fail; someone can also add a member by hand. The refresh compares the group with the licenses and fixes the drift, so the roles never accumulate:

| Group member… | Action |
|---|---|
| without an active license in the portal **and** not licensed in Google, not in flight (seat reserved / buffered) | 🔴 removed |
| licensed in Google outside the portal (open GOOGLE_ONLY deviation) | 🟠 kept until an admin resolves the deviation |
| licensed in the portal and in Google but missing from the group | 🟢 added back |
| owner / manager of the group (the portal service accounts) | never touched |

Google is part of the reference, so a database problem can never trigger a mass revocation. At most 200 changes per run (the rest at the next run); every change is in the audit log (`credentials.reconcile`) and the job summary (`iam_revoked=… iam_granted=…`).

### What exactly do these roles give?

| Role | Scope | Gives |
|---|---|---|
| `roles/cloudaicompanion.user` | **Gemini for Google Cloud** on the Gemini project | Gemini Code Assist in the IDE — and the other Gemini for Google Cloud entry points of that project (Gemini in the Cloud console, Gemini CLI signed in with the Code Assist license). Not specific to the IDE plugin. |
| `roles/serviceusage.serviceUsageConsumer` | the Gemini project | Permission to *consume* the APIs enabled on that project (quota / billing attribution). Generic, but harmless on a **dedicated project with only `cloudaicompanion.googleapis.com` enabled and no resources**: there is nothing else to call. |

Neither role gives access to code, data or resources elsewhere. Without a license, the roles alone do not give Gemini Code Assist Standard / Enterprise. 🟠 The policy *"no command-line usage"* (Gemini CLI) cannot be enforced by IAM — the same license and role allow it; it stays a usage rule (confirmation e-mail, training).

### Why not grant the roles to everyone?

Technically possible (`domain:example.com` on the project) and it would remove step X entirely — `CREDENTIALS_MODE=none` supports it. It is **not recommended** for v1:

| Risk | Detail |
|---|---|
| 🔴 Auto-assignment bypass | If someone switches the pool to *automatic* assignment, every employee holding the role gets a license on first use — training and directory checks are bypassed and the pool drains. With the group, the role exists only for portal-approved users. |
| 🟠 Service Usage Consumer on the project | Lets anyone consume *any* API enabled on that project, billed to it. Acceptable only on a dedicated project with nothing but `cloudaicompanion.googleapis.com` enabled. |
| 🟠 Other Gemini for Google Cloud features | The role is not specific to Code Assist; access to other Gemini features of the project would become company-wide. |
| 🟠 Audit | "Who may use Gemini Code Assist" would no longer be answerable from IAM. |

If the security team accepts these points (dedicated project, manual pool locked by org policy), set `CREDENTIALS_MODE=none` and grant the two roles to the domain.

## Enterprise SSO
Identity-Aware Proxy on the `gcalm-web` Cloud Run service; the app verifies the `X-Goog-IAP-JWT-Assertion` JWT (ES256, Google public keys, audience `/projects/NUMBER/locations/REGION/services/gcalm-web`).

## References
- Manage Gemini Code Assist Standard and Enterprise licenses — https://cloud.google.com/gemini/docs/manage-licenses
- Set up Gemini Code Assist Standard and Enterprise — https://cloud.google.com/gemini/docs/codeassist/set-up-gemini
- Gemini Code Assist overview — https://cloud.google.com/gemini/docs/codeassist/overview
- Cloud Commerce Consumer Procurement API — https://cloud.google.com/marketplace/docs/reference/consumerprocurement/rest
