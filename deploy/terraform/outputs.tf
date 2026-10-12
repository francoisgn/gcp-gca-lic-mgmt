output "portal_url" {
  value = google_cloud_run_v2_service.web.uri
}

output "jobs" {
  description = "Cloud Run jobs executed by Cloud Scheduler."
  value       = [for j in google_cloud_run_v2_job.jobs : j.name]
}

output "artifact_registry" {
  value = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.images.repository_id}"
}

output "service_accounts" {
  value = {
    web       = google_service_account.web.email
    worker    = google_service_account.worker.email
    scheduler = google_service_account.scheduler.email
  }
}

output "secrets_to_fill" {
  description = "Secrets whose value must be added manually (gcloud secrets versions add)."
  value       = ["gcalm-directory-api-key", "gcalm-tracker-token", "gcalm-smtp-password"]
}

output "gitlab_ci_variables" {
  description = "CI/CD variables to set in the GitLab project (Settings → CI/CD → Variables). None is secret."
  value = local.cicd == 1 ? {
    GCP_PROJECT_ID   = var.project_id
    GCP_REGION       = var.region
    GCP_WIF_PROVIDER = google_iam_workload_identity_pool_provider.gitlab[0].name
    GCP_DEPLOYER_SA  = google_service_account.deployer[0].email
  } : null
}
