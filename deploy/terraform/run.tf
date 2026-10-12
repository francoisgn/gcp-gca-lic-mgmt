# =============================================================================
# Cloud Run services (same image, two modes)
# =============================================================================

locals {
  common_env = merge({
    APP_ENV            = "prod"
    TIMEZONE           = var.time_zone
    BASE_URL           = local.base_url
    GOOGLE_MODE        = "real"
    BILLING_ACCOUNT_ID = var.billing_account_id
    QUOTA_PROJECT      = var.project_id
    GCA_PROJECT_ID     = var.gca_project_id
    CREDENTIALS_MODE   = "group"
    CREDENTIALS_GROUP  = var.gca_users_group_email
    DIRECTORY_MODE     = "http"
    DIRECTORY_URL      = var.directory_url
    MAIL_MODE          = "smtp"
    MAIL_FROM          = var.mail_from
    TRACKER_MODE       = var.tracker_mode
  }, var.tracker_env)

  secret_env = {
    DATABASE_URL      = "database-url"
    DIRECTORY_API_KEY = "directory-api-key"
    JIRA_TOKEN        = "tracker-token"
    GITLAB_TOKEN      = "tracker-token"
    SMTP_PASSWORD     = "smtp-password"
  }
}

# --- Portal (behind IAP) --------------------------------------------------------

resource "google_cloud_run_v2_service" "web" {
  provider     = google-beta
  name         = local.web_name
  location     = var.region
  ingress      = "INGRESS_TRAFFIC_ALL"
  launch_stage = "BETA"
  # Identity-Aware Proxy directly on Cloud Run: Google SSO, no load balancer.
  iap_enabled = true

  template {
    service_account = google_service_account.web.email
    scaling {
      min_instance_count = 0
      max_instance_count = 5
    }
    max_instance_request_concurrency = 80
    timeout                          = "300s"
    containers {
      image = var.image
      ports {
        container_port = 8080
      }
      resources {
        limits = { cpu = "1", memory = "512Mi" }
      }
      env {
        name  = "APP_MODE"
        value = "web"
      }
      env {
        name  = "AUTH_MODE"
        value = "iap"
      }
      env {
        name  = "IAP_AUDIENCE"
        value = "/projects/${data.google_project.portal.number}/locations/${var.region}/services/${local.web_name}"
      }
      dynamic "env" {
        for_each = local.common_env
        content {
          name  = env.key
          value = env.value
        }
      }
      dynamic "env" {
        for_each = local.secret_env
        content {
          name = env.key
          value_source {
            secret_key_ref {
              secret  = google_secret_manager_secret.secrets[env.value].secret_id
              version = "latest"
            }
          }
        }
      }
      volume_mounts {
        name       = "cloudsql"
        mount_path = "/cloudsql"
      }
      startup_probe {
        http_get {
          path = "/healthz"
        }
      }
    }
    volumes {
      name = "cloudsql"
      cloud_sql_instance {
        instances = [google_sql_database_instance.main.connection_name]
      }
    }
  }
  depends_on = [google_secret_manager_secret_iam_member.runtime, google_secret_manager_secret_version.database_url]
}

# Who may reach the portal through IAP.
resource "google_iap_web_cloud_run_service_iam_member" "portal_users" {
  for_each               = toset(var.portal_users)
  provider               = google-beta
  location               = var.region
  cloud_run_service_name = google_cloud_run_v2_service.web.name
  role                   = "roles/iap.httpsResourceAccessor"
  member                 = each.value
}

# The IAP service agent invokes the service on behalf of the users.
resource "google_cloud_run_v2_service_iam_member" "iap_invoker" {
  name     = google_cloud_run_v2_service.web.name
  location = var.region
  role     = "roles/run.invoker"
  member   = "serviceAccount:service-${data.google_project.portal.number}@gcp-sa-iap.iam.gserviceaccount.com"
}

# --- Worker (scheduled jobs, private) ------------------------------------------------

resource "google_cloud_run_v2_service" "worker" {
  name     = local.worker_name
  location = var.region
  # Cloud Scheduler is outside the VPC: ingress must be "all", access is
  # restricted by Cloud Run IAM (only the scheduler SA is run.invoker).
  ingress = "INGRESS_TRAFFIC_ALL"

  template {
    service_account = google_service_account.worker.email
    scaling {
      min_instance_count = 0
      max_instance_count = 1
    }
    max_instance_request_concurrency = 4
    timeout                          = "900s"
    containers {
      image = var.image
      resources {
        limits = { cpu = "1", memory = "512Mi" }
      }
      env {
        name  = "APP_MODE"
        value = "worker"
      }
      env {
        name  = "AUTH_MODE"
        value = "iap" # unused by the worker routes, keeps the prod config consistent
      }
      env {
        name  = "IAP_AUDIENCE"
        value = "/projects/${data.google_project.portal.number}/locations/${var.region}/services/${local.web_name}"
      }
      env {
        name  = "JOBS_AUDIENCE"
        value = local.worker_url
      }
      env {
        name  = "JOBS_INVOKER"
        value = google_service_account.scheduler.email
      }
      dynamic "env" {
        for_each = local.common_env
        content {
          name  = env.key
          value = env.value
        }
      }
      dynamic "env" {
        for_each = local.secret_env
        content {
          name = env.key
          value_source {
            secret_key_ref {
              secret  = google_secret_manager_secret.secrets[env.value].secret_id
              version = "latest"
            }
          }
        }
      }
      volume_mounts {
        name       = "cloudsql"
        mount_path = "/cloudsql"
      }
    }
    volumes {
      name = "cloudsql"
      cloud_sql_instance {
        instances = [google_sql_database_instance.main.connection_name]
      }
    }
  }
  depends_on = [google_secret_manager_secret_iam_member.runtime, google_secret_manager_secret_version.database_url]
}
