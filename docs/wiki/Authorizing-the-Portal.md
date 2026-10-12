# Authorizing the portal to manage licenses

**No user account, no key file.** The portal runs on Cloud Run as two service accounts — `gcalm-web` (portal) and `gcalm-worker` (scheduled jobs). Cloud Run hands them short-lived Google tokens automatically (Application Default Credentials). Authorizing the portal = granting **these two identities** the rights below, once.

```
 gcalm-web ──────┐
                 ├──▶ Cloud Commerce Consumer Procurement API   ◀── custom role gcaLicensePortal
 gcalm-worker ───┤                                                  on the BILLING ACCOUNT
                 └──▶ Cloud Identity Groups API                 ◀── MANAGER of the group gca-users@
                                                                     │ bound once on the GEMINI PROJECT:
                                                                     ├─ roles/cloudaicompanion.user
                                                                     └─ roles/serviceusage.serviceUsageConsumer
```

## What to grant

| # | Right | On | Granted by | Terraform |
|---|---|---|---|---|
| 1 | Custom role **`gcaLicensePortal`** (7 permissions below) — defined at organization level | **billing account** holding the Gemini Code Assist subscription | Organization Role Administrator (role) + **Billing Account Administrator** (binding) | `iam.tf` |
| 2 | **MANAGER** of the IAM bundle group (`CREDENTIALS_GROUP`) | the Google group | Group owner or Workspace / Cloud Identity *Groups Admin* | `group.tf` (when Terraform creates the group) |
| 3 | The group gets `roles/cloudaicompanion.user` + `roles/serviceusage.serviceUsageConsumer` (once) | **Gemini project** (`GCA_PROJECT_ID`) | Project IAM Admin | `group.tf` |
| 4 | `roles/serviceusage.serviceUsageConsumer` (API quota) + Cloud SQL client, log / metric writer | **portal project** | Project IAM Admin | `iam.tf` |
| 5 | APIs enabled: `cloudcommerceconsumerprocurement.googleapis.com`, `cloudidentity.googleapis.com` | portal project | Project owner | `main.tf` |

Custom role `gcaLicensePortal` — read the subscription, read the pool, assign / unassign / list licensed users, **nothing else**:

```
billing.accounts.get
consumerprocurement.orders.get
consumerprocurement.orders.list
consumerprocurement.licensePools.get
consumerprocurement.licensePools.assign
consumerprocurement.licensePools.unassign
consumerprocurement.licensePools.enumerateLicensedUsers
```

| The portal can | The portal cannot |
|---|---|
| 🟢 see the Gemini Code Assist orders and their seats | 🔴 buy, modify, renew or cancel a subscription |
| 🟢 assign / remove a license, list licensed users | 🔴 change the assignment mode of the pool |
| 🟢 add / remove members of **one** group | 🔴 change the roles given to that group, touch any other IAM policy |

## With Terraform

`terraform apply` (see [[Deployment]]) creates the service accounts and grants 1, 3, 4 and 5; it grants 2 too when it creates the group (`create_gca_users_group = true`). The person applying needs: Owner on the portal project, *Organization Role Administrator*, *Billing Account Administrator* and, for the group, *Groups Admin*.

## Without Terraform (gcloud)

```bash
ORG=123456789012                 # organization id
BILLING=000000-000000-000000     # billing account of the subscription
PROJECT=my-gca-portal            # portal project
GCA_PROJECT=my-gemini-project    # project users select in the IDE
GROUP=gca-users@example.com      # IAM bundle group

# 1. Custom role (organization) + binding on the billing account
gcloud iam roles create gcaLicensePortal --organization=$ORG \
  --title="Gemini Code Assist license portal" \
  --permissions=billing.accounts.get,consumerprocurement.orders.get,consumerprocurement.orders.list,consumerprocurement.licensePools.get,consumerprocurement.licensePools.assign,consumerprocurement.licensePools.unassign,consumerprocurement.licensePools.enumerateLicensedUsers
for SA in gcalm-web gcalm-worker; do
  gcloud billing accounts add-iam-policy-binding $BILLING \
    --member="serviceAccount:$SA@$PROJECT.iam.gserviceaccount.com" \
    --role="organizations/$ORG/roles/gcaLicensePortal"
done

# 2. The service accounts manage the group membership
for SA in gcalm-web gcalm-worker; do
  gcloud identity groups memberships add --group-email=$GROUP \
    --member-email=$SA@$PROJECT.iam.gserviceaccount.com --roles=MEMBER,MANAGER
done

# 3. The group carries the two Gemini roles on the Gemini project (once)
for ROLE in roles/cloudaicompanion.user roles/serviceusage.serviceUsageConsumer; do
  gcloud projects add-iam-policy-binding $GCA_PROJECT --member=group:$GROUP --role=$ROLE
done
```

## Check that it works

| Check | Expected | If not |
|---|---|---|
| *Observability → Force subscription refresh* (super admin) | the active subscription with its dates and seats | `PERMISSION_DENIED` in *Scheduled jobs* → right 1 or 5 missing |
| Request a license with a test account | license active; the user appears in the group (`gcloud identity groups memberships list --group-email=$GROUP`) | step *Cloud credentials* in the buffer → right 2 missing |
| Remove that license (*Admin*) | the user leaves the group | idem |
| From a workstation (needs *Service Account Token Creator* on the SA) | JSON list of orders | — |

```bash
curl -s -H "Authorization: Bearer $(gcloud auth print-access-token --impersonate-service-account=gcalm-worker@$PROJECT.iam.gserviceaccount.com)" \
  -H "X-Goog-User-Project: $PROJECT" \
  "https://cloudcommerceconsumerprocurement.googleapis.com/v1/billingAccounts/$BILLING/orders"
```

## CI/CD identity

Deliveries from GitLab use a third identity, `gcalm-deployer`, reached without key through Workload Identity Federation and limited to image push and rollout — see [[Continuous Integration]].

## Why service accounts and not an account "with rights"?

| Option | Verdict |
|---|---|
| Personal account of an administrator | 🔴 MFA, leaves the company, actions attributed to a person |
| Service account **key file** (JSON) | 🔴 long-lived secret to store, rotate and protect |
| **Cloud Run service account (ADC)** — implemented | 🟢 no secret at all, short-lived tokens, every call audited under `gcalm-web` / `gcalm-worker` in Cloud Audit Logs |
