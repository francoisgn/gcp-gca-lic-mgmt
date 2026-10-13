# =============================================================================
# Cloud Run services (same image, two modes)
# =============================================================================

locals {
  # Integration endpoints (directory, GitLab, SMTP) live in config/settings.yaml
  # (versioned); only deployment-specific values are passed here.
  common_env = {
    APP_ENV            = "prod"
    SETTINGS_FILE      = "/app/config/settings.yaml"
    TIMEZONE           = var.time_zone
    BASE_URL           = local.base_url
    GOOGLE_MODE        = "real"
    BILLING_ACCOUNT_ID = var.billing_account_id
    QUOTA_PROJECT      = var.project_id
    GCA_PROJECT_ID     = var.gca_project_id
    CREDENTIALS_MODE   = "group"
    CREDENTIALS_GROUP  = var.gca_users_group_email
  }

  # Env var name (as referenced by *_secret_env in settings.yaml) => secret.
  secret_env = {
    DATABASE_URL      = "database-url"
    DIRECTORY_API_KEY = "directory-api-key"
    GITLAB_TOKEN      = "tracker-token"
    SMTP_PASSWORD     = "smtp-password" # gitleaks:allow (secret NAME, not a value)
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
  # Terraform creates the service with var.image; later images are rolled out
  # by CI/CD (scripts/rollout.sh), not by Terraform.
  lifecycle {
    ignore_changes = [template[0].containers[0].image, client, client_version]
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

# --- Scheduled jobs: one Cloud Run job per task (gcalm job <name>) -----------
# Executed by Cloud Scheduler (scheduler.tf) through the Cloud Run Admin API.
# A failed run exits non-zero and is retried once by Cloud Run.

resource "google_cloud_run_v2_job" "jobs" {
  for_each = local.jobs
  name     = "gcalm-${each.key}"
  location = var.region

  template {
    task_count = 1
    template {
      service_account = google_service_account.worker.email
      max_retries     = 1
      timeout         = each.value.timeout
      containers {
        image = var.image
        args  = ["job", each.key]
        resources {
          limits = { cpu = "1", memory = "512Mi" }
        }
        env {
          name  = "APP_MODE"
          value = "worker"
        }
        env {
          name  = "AUTH_MODE"
          value = "iap" # unused by jobs, keeps the prod config consistent
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
      }
      volumes {
        name = "cloudsql"
        cloud_sql_instance {
          instances = [google_sql_database_instance.main.connection_name]
        }
      }
    }
  }
  # Terraform creates the job with var.image; later images are rolled out by
  # CI/CD (scripts/rollout.sh), not by Terraform.
  lifecycle {
    ignore_changes = [template[0].template[0].containers[0].image, client, client_version]
  }
  depends_on = [google_secret_manager_secret_iam_member.runtime, google_secret_manager_secret_version.database_url]
}

# Cloud Scheduler may only execute the jobs.
resource "google_cloud_run_v2_job_iam_member" "scheduler_runs_jobs" {
  for_each = local.jobs
  name     = google_cloud_run_v2_job.jobs[each.key].name
  location = var.region
  role     = "roles/run.invoker" # includes run.jobs.run
  member   = "serviceAccount:${google_service_account.scheduler.email}"
}
