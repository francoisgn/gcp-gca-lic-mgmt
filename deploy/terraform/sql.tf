# =============================================================================
# Cloud SQL for PostgreSQL 16
# Relational model (queue positions, history, KPIs per day) => PostgreSQL.
# Volume is tiny (a few thousand rows / month): 1 vCPU is plenty.
# =============================================================================

resource "google_sql_database_instance" "main" {
  name             = "gcalm-pg"
  database_version = "POSTGRES_16"
  region           = var.region
  settings {
    tier              = var.db_tier
    edition           = "ENTERPRISE"
    availability_type = "ZONAL" # switch to REGIONAL for HA
    disk_autoresize   = true
    backup_configuration {
      enabled                        = true
      point_in_time_recovery_enabled = true
      start_time                     = "02:00"
      backup_retention_settings {
        retained_backups = 14
      }
    }
    insights_config {
      query_insights_enabled = true
    }
    ip_configuration {
      ipv4_enabled = true # reached through the Cloud SQL connector of Cloud Run only (no authorised networks)
      ssl_mode     = "ENCRYPTED_ONLY"
    }
    maintenance_window {
      day  = 7
      hour = 3
    }
  }
  deletion_protection = true
  depends_on          = [google_project_service.apis]
}

resource "google_sql_database" "app" {
  name     = "gcalm"
  instance = google_sql_database_instance.main.name
}

resource "random_password" "db" {
  length  = 32
  special = false
}

resource "google_sql_user" "app" {
  name     = "gcalm"
  instance = google_sql_database_instance.main.name
  password = random_password.db.result
}
