# =============================================================================
# Gemini Code Assist license portal — Google Cloud infrastructure
#
#   Cloud Run "gcalm-web"    portal, behind Identity-Aware Proxy (Google SSO)
#   Cloud Run jobs "gcalm-*" scheduled jobs, executed by Cloud Scheduler only
#   Cloud SQL PostgreSQL 16  state (requests, queue, buffer, KPIs)
#   Cloud Scheduler          buffer-retry, queue, subscription-refresh, purge,
#                            directory-recheck, housekeeping
#   Workload Identity Fed.   keyless CI/CD from GitLab (cicd.tf, optional)
#   Secret Manager           database URL, directory API key, tracker token, SMTP password
#   Google group             IAM bundle granted to licensed users
# =============================================================================

data "google_project" "portal" {
  project_id = var.project_id
}

locals {
  apis = [
    "run.googleapis.com",
    "sqladmin.googleapis.com",
    "cloudscheduler.googleapis.com",
    "secretmanager.googleapis.com",
    "iap.googleapis.com",
    "artifactregistry.googleapis.com",
    "cloudidentity.googleapis.com",
    "cloudcommerceconsumerprocurement.googleapis.com",
    "logging.googleapis.com",
    "monitoring.googleapis.com",
    "iam.googleapis.com",            # workload identity federation (CI/CD)
    "sts.googleapis.com",            # token exchange (CI/CD)
    "iamcredentials.googleapis.com", # service account impersonation (CI/CD)
  ]
  web_name = "gcalm-web"
  # Deterministic Cloud Run URL (https://SERVICE-PROJECT_NUMBER.REGION.run.app).
  web_url  = "https://${local.web_name}-${data.google_project.portal.number}.${var.region}.run.app"
  base_url = var.base_url != "" ? var.base_url : local.web_url
}

resource "google_project_service" "apis" {
  for_each           = toset(local.apis)
  service            = each.value
  disable_on_destroy = false
}

# The Gemini for Google Cloud API on the project selected by users in their IDE.
resource "google_project_service" "gca" {
  project            = var.gca_project_id
  service            = "cloudaicompanion.googleapis.com"
  disable_on_destroy = false
}

resource "google_artifact_registry_repository" "images" {
  location      = var.region
  repository_id = "gcalm"
  format        = "DOCKER"
  description   = "Gemini Code Assist license portal images"
  depends_on    = [google_project_service.apis]
}
