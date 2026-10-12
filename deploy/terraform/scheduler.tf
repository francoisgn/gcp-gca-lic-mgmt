# =============================================================================
# Cloud Scheduler: one HTTP job per portal job, OIDC-authenticated.
# Schedules mirror cmd/gcalm/main.go (jobList).
# =============================================================================

locals {
  jobs = {
    "buffer-retry"         = { schedule = "*/7 * * * *", deadline = "360s" }    # buffer every 7 min
    "queue"                = { schedule = "3-59/7 * * * *", deadline = "360s" } # waiting list
    "subscription-refresh" = { schedule = "0 */3 * * *", deadline = "900s" }    # every 3 h
    "purge"                = { schedule = "15 * * * *", deadline = "900s" }     # hourly
    "directory-recheck"    = { schedule = "30 2 * * *", deadline = "1800s" }    # nightly, monthly per license
    "housekeeping"         = { schedule = "30 3 * * *", deadline = "600s" }     # daily
  }
}

resource "google_cloud_scheduler_job" "jobs" {
  for_each         = local.jobs
  name             = "gcalm-${each.key}"
  region           = var.region
  schedule         = each.value.schedule
  time_zone        = var.time_zone
  attempt_deadline = each.value.deadline
  retry_config {
    retry_count          = 1
    min_backoff_duration = "30s"
  }
  http_target {
    http_method = "POST"
    uri         = "${local.worker_url}/jobs/${each.key}"
    oidc_token {
      service_account_email = google_service_account.scheduler.email
      audience              = local.worker_url
    }
  }
  depends_on = [google_project_service.apis, google_cloud_run_v2_service_iam_member.scheduler_invokes_worker]
}
