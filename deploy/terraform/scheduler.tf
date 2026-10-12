# =============================================================================
# Cloud Scheduler: executes one Cloud Run job per portal job (Cloud Run Admin
# API, OAuth token of the scheduler service account). Schedules mirror
# cmd/gcalm/main.go (jobList); the task timeout leaves 2 minutes of margin
# over the job timeout of the application.
# =============================================================================

locals {
  jobs = {
    "buffer-retry"         = { schedule = "*/7 * * * *", timeout = "480s" }    # buffer every 7 min
    "queue"                = { schedule = "3-59/7 * * * *", timeout = "480s" } # waiting list
    "subscription-refresh" = { schedule = "0 */3 * * *", timeout = "1020s" }   # every 3 h
    "purge"                = { schedule = "15 * * * *", timeout = "1020s" }    # hourly
    "directory-recheck"    = { schedule = "30 2 * * *", timeout = "1920s" }    # nightly, monthly per license
    "housekeeping"         = { schedule = "30 3 * * *", timeout = "720s" }     # daily
  }
}

resource "google_cloud_scheduler_job" "jobs" {
  for_each         = local.jobs
  name             = "gcalm-${each.key}"
  region           = var.region
  schedule         = each.value.schedule
  time_zone        = var.time_zone
  attempt_deadline = "120s" # starting the execution only; the run itself is followed by Cloud Run
  retry_config {
    retry_count          = 1
    min_backoff_duration = "30s"
  }
  http_target {
    http_method = "POST"
    uri         = "https://run.googleapis.com/v2/projects/${var.project_id}/locations/${var.region}/jobs/${google_cloud_run_v2_job.jobs[each.key].name}:run"
    oauth_token {
      service_account_email = google_service_account.scheduler.email
      scope                 = "https://www.googleapis.com/auth/cloud-platform"
    }
  }
  depends_on = [google_project_service.apis, google_cloud_run_v2_job_iam_member.scheduler_runs_jobs]
}
