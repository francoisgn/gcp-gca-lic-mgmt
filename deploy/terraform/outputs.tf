output "portal_url" {
  value = google_cloud_run_v2_service.web.uri
}

output "worker_url" {
  value = google_cloud_run_v2_service.worker.uri
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
