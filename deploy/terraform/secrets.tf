# =============================================================================
# Secrets (Secret Manager), injected as environment variables by Cloud Run.
# Only the database URL is generated here; the other values are added
# out-of-band (gcloud secrets versions add ...) and never stored in git.
# =============================================================================

resource "google_secret_manager_secret" "secrets" {
  for_each  = toset(["database-url", "directory-api-key", "tracker-token", "smtp-password"])
  secret_id = "gcalm-${each.value}"
  replication {
    auto {}
  }
  depends_on = [google_project_service.apis]
}

resource "google_secret_manager_secret_version" "database_url" {
  secret = google_secret_manager_secret.secrets["database-url"].id
  # Unix socket mounted by Cloud Run at /cloudsql/<connection name>.
  secret_data = "host=/cloudsql/${google_sql_database_instance.main.connection_name} user=${google_sql_user.app.name} password=${random_password.db.result} dbname=${google_sql_database.app.name} sslmode=disable"
}

resource "google_secret_manager_secret_iam_member" "runtime" {
  for_each = {
    for pair in setproduct(keys(google_secret_manager_secret.secrets), keys(local.runtime_sas)) :
    "${pair[0]}-${pair[1]}" => { secret = pair[0], sa = local.runtime_sas[pair[1]] }
  }
  secret_id = google_secret_manager_secret.secrets[each.value.secret].id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${each.value.sa}"
}
