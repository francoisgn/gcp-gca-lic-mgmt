# =============================================================================
# Service accounts and permissions
# =============================================================================

resource "google_service_account" "web" {
  account_id   = "gcalm-web"
  display_name = "GCA license portal — web"
}

resource "google_service_account" "worker" {
  account_id   = "gcalm-worker"
  display_name = "GCA license portal — scheduled jobs"
}

resource "google_service_account" "scheduler" {
  account_id   = "gcalm-scheduler"
  display_name = "GCA license portal — Cloud Scheduler invoker"
}

locals {
  runtime_sas = {
    web    = google_service_account.web.email
    worker = google_service_account.worker.email
  }
}

# Runtime permissions shared by web and worker.
resource "google_project_iam_member" "runtime" {
  for_each = {
    for pair in setproduct(keys(local.runtime_sas), [
      "roles/cloudsql.client",
      "roles/logging.logWriter",
      "roles/monitoring.metricWriter",
      "roles/serviceusage.serviceUsageConsumer", # quota project of the Procurement API
    ]) : "${pair[0]}-${pair[1]}" => { sa = local.runtime_sas[pair[0]], role = pair[1] }
  }
  project = var.project_id
  role    = each.value.role
  member  = "serviceAccount:${each.value.sa}"
}

# Least-privilege role to manage Gemini Code Assist licenses
# (Cloud Commerce Consumer Procurement API). Custom roles usable on a billing
# account must be defined at organization level.
resource "google_organization_iam_custom_role" "license_manager" {
  org_id      = var.org_id
  role_id     = "gcaLicensePortal"
  title       = "Gemini Code Assist license portal"
  description = "List orders, read the license pool, assign / unassign / enumerate licenses."
  permissions = [
    "billing.accounts.get",
    "consumerprocurement.orders.get",
    "consumerprocurement.orders.list",
    "consumerprocurement.licensePools.get",
    "consumerprocurement.licensePools.assign",
    "consumerprocurement.licensePools.unassign",
    "consumerprocurement.licensePools.enumerateLicensedUsers",
  ]
}

resource "google_billing_account_iam_member" "license_manager" {
  for_each           = local.runtime_sas
  billing_account_id = var.billing_account_id
  role               = google_organization_iam_custom_role.license_manager.name
  member             = "serviceAccount:${each.value}"
}

# Cloud Scheduler may only invoke the worker.
resource "google_cloud_run_v2_service_iam_member" "scheduler_invokes_worker" {
  name     = google_cloud_run_v2_service.worker.name
  location = var.region
  role     = "roles/run.invoker"
  member   = "serviceAccount:${google_service_account.scheduler.email}"
}
