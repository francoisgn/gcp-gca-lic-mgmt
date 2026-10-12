# Deployment

Infrastructure as code: `deploy/terraform` (providers google / google-beta 8.x, validated with `terraform validate`).

## 1. Prerequisites (one-off, with the platform team)

| # | Action | Who |
|---|---|---|
| 1 | Project for the portal; project for Gemini (`gca_project_id`, can be the same) | platform |
| 2 | Gemini Code Assist subscription purchased; license pool in **manual** assignment mode | licensing owner |
| 3 | Rights to apply: Owner on the project, `roles/iam.organizationRoleAdmin` (custom role), Billing Account Administrator (IAM on the billing account) | platform |
| 4 | IAM bundle group: either let Terraform create it (`create_gca_users_group = true`, `customer_id`) or create it in the Admin console and add the two portal service accounts as **managers** | Workspace admin |
| 5 | SMTP relay allowing the portal sender; Jira (or GitLab) technical account and token | Workspace admin / tooling |
| 6 | Enterprise directory endpoint + API key | directory owner |
| 7 | Remote Terraform state bucket (`backend "gcs"` in `versions.tf`) | platform |

## 2. Build the image

```bash
REGION=europe-west1; PROJECT=my-gca-portal; VERSION=1.0.0
gcloud artifacts repositories create gcalm --repository-format=docker --location=$REGION   # or let Terraform do it
gcloud builds submit --tag $REGION-docker.pkg.dev/$PROJECT/gcalm/gcalm:$VERSION .
```

## 3. Apply

```bash
cd deploy/terraform
cp example.tfvars prod.tfvars     # edit (git-ignored)
terraform init
terraform apply -var-file=prod.tfvars -var image=$REGION-docker.pkg.dev/$PROJECT/gcalm/gcalm:$VERSION
```

Creates: APIs, Artifact Registry, 3 service accounts, custom role + billing account bindings, group bindings on the Gemini project, Cloud SQL (PostgreSQL 16, PITR), secrets, Cloud Run `gcalm-web` (IAP) and `gcalm-worker`, IAP access for `portal_users`, Cloud Scheduler jobs, log-based metrics and alert policies.

## 4. Fill the secrets

```bash
printf '%s' "$DIRECTORY_KEY" | gcloud secrets versions add gcalm-directory-api-key --data-file=-
printf '%s' "$JIRA_TOKEN"    | gcloud secrets versions add gcalm-tracker-token     --data-file=-
printf '%s' "$SMTP_PASSWORD" | gcloud secrets versions add gcalm-smtp-password     --data-file=-
gcloud run services update gcalm-web    --region $REGION   # pick up the new versions
gcloud run services update gcalm-worker --region $REGION
```

## 5. First run checklist

- [ ] Open the portal: SSO through IAP, top bar counter shows `assigned / total`.
- [ ] Super admin → *Observability* → **Force subscription refresh**: the active order appears with its dates and seats.
- [ ] Existing licenses (assigned before the portal) appear as **GOOGLE_ONLY deviations** → *Back-port* them (or let them be removed by the 7-day rule).
- [ ] Request a license with a test account; check the group membership and the confirmation e-mail.
- [ ] *Scheduled jobs* table: every job ran once (*Run now*).

## Upgrades
Build a new image, `terraform apply -var image=…`. Migrations run automatically at start-up (advisory lock, idempotent). Roles / policy changes = pull request + new image.
