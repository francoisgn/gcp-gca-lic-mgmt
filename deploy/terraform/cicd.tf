# =============================================================================
# Keyless CI/CD from GitLab — Workload Identity Federation.
#
# GitLab issues an OIDC token to each pipeline job; Google exchanges it for a
# short-lived token of the gcalm-deployer service account, only for pipelines
# of ONE GitLab project running on a protected branch or tag. No service
# account key exists anywhere.
#
# The deployer pushes images and rolls them out to Cloud Run; it cannot
# change the infrastructure (terraform apply stays a reviewed, manual step).
# Created only when var.gitlab_project_path is set.
# =============================================================================

locals {
  cicd = var.gitlab_project_path != "" ? 1 : 0
}

resource "google_iam_workload_identity_pool" "gitlab" {
  count                     = local.cicd
  workload_identity_pool_id = "gitlab"
  display_name              = "GitLab CI/CD"
  description               = "Pipelines of ${var.gitlab_project_path}"
  depends_on                = [google_project_service.apis]
}

resource "google_iam_workload_identity_pool_provider" "gitlab" {
  count                              = local.cicd
  workload_identity_pool_id          = google_iam_workload_identity_pool.gitlab[0].workload_identity_pool_id
  workload_identity_pool_provider_id = "gitlab"
  display_name                       = "GitLab"
  attribute_mapping = {
    "google.subject"          = "assertion.sub" # project_path:<group/project>:ref_type:<branch|tag>:ref:<name>
    "attribute.project_path"  = "assertion.project_path"
    "attribute.ref"           = "assertion.ref"
    "attribute.ref_type"      = "assertion.ref_type"
    "attribute.ref_protected" = "assertion.ref_protected"
  }
  # Only this project, only protected branches and tags.
  attribute_condition = "assertion.project_path == \"${var.gitlab_project_path}\" && assertion.ref_protected == \"true\""
  oidc {
    issuer_uri = var.gitlab_url
    # Internal GitLab not reachable by Google: provide its public keys.
    jwks_json = var.gitlab_jwks_json != "" ? var.gitlab_jwks_json : null
  }
}

resource "google_service_account" "deployer" {
  count        = local.cicd
  account_id   = "gcalm-deployer"
  display_name = "GCA license portal — CI/CD deployer (GitLab, keyless)"
}

# GitLab pipelines of the project may act as the deployer.
resource "google_service_account_iam_member" "gitlab_impersonates_deployer" {
  count              = local.cicd
  service_account_id = google_service_account.deployer[0].name
  role               = "roles/iam.workloadIdentityUser"
  member             = "principalSet://iam.googleapis.com/${google_iam_workload_identity_pool.gitlab[0].name}/attribute.project_path/${var.gitlab_project_path}"
}

# Push images to the portal repository only.
resource "google_artifact_registry_repository_iam_member" "deployer_pushes" {
  count      = local.cicd
  location   = google_artifact_registry_repository.images.location
  repository = google_artifact_registry_repository.images.name
  role       = "roles/artifactregistry.writer"
  member     = "serviceAccount:${google_service_account.deployer[0].email}"
}

# Roll new revisions out to the Cloud Run service and jobs.
resource "google_project_iam_member" "deployer_rolls_out" {
  count   = local.cicd
  project = var.project_id
  role    = "roles/run.developer"
  member  = "serviceAccount:${google_service_account.deployer[0].email}"
}

# Deploying a revision that runs as a service account requires "act as" it.
resource "google_service_account_iam_member" "deployer_acts_as_runtime" {
  for_each = local.cicd == 1 ? {
    web    = google_service_account.web.name
    worker = google_service_account.worker.name
  } : {}
  service_account_id = each.value
  role               = "roles/iam.serviceAccountUser"
  member             = "serviceAccount:${google_service_account.deployer[0].email}"
}
