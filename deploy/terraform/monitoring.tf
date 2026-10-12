# =============================================================================
# Alerting on structured logs (Cloud Logging).
# =============================================================================

resource "google_logging_metric" "job_errors" {
  name   = "gcalm/job_errors"
  filter = "resource.type=\"cloud_run_revision\" AND jsonPayload.message=\"job finished\" AND jsonPayload.status=\"ERROR\""
  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "INT64"
    labels {
      key        = "job"
      value_type = "STRING"
    }
  }
  label_extractors = { job = "EXTRACT(jsonPayload.job)" }
}

resource "google_logging_metric" "google_timeouts" {
  name   = "gcalm/google_timeouts"
  filter = "resource.type=\"cloud_run_revision\" AND jsonPayload.message=\"outbound call failed\" AND jsonPayload.outcome=\"TIMEOUT\" AND jsonPayload.target=~\"GOOGLE_.*\""
  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "INT64"
  }
}

resource "google_monitoring_notification_channel" "email" {
  display_name = "GCA license portal"
  type         = "email"
  labels       = { email_address = var.alert_email }
}

resource "google_monitoring_alert_policy" "job_errors" {
  display_name = "GCA license portal — scheduled job failing"
  combiner     = "OR"
  conditions {
    display_name = "job errors"
    condition_threshold {
      filter          = "metric.type=\"logging.googleapis.com/user/${google_logging_metric.job_errors.name}\" AND resource.type=\"cloud_run_revision\""
      comparison      = "COMPARISON_GT"
      threshold_value = 0
      duration        = "0s"
      aggregations {
        alignment_period   = "900s"
        per_series_aligner = "ALIGN_SUM"
      }
    }
  }
  notification_channels = [google_monitoring_notification_channel.email.id]
}

resource "google_monitoring_alert_policy" "google_timeouts" {
  display_name = "GCA license portal — Google API timeouts"
  combiner     = "OR"
  conditions {
    display_name = "more than 10 Google timeouts in 30 min"
    condition_threshold {
      filter          = "metric.type=\"logging.googleapis.com/user/${google_logging_metric.google_timeouts.name}\" AND resource.type=\"cloud_run_revision\""
      comparison      = "COMPARISON_GT"
      threshold_value = 10
      duration        = "0s"
      aggregations {
        alignment_period   = "1800s"
        per_series_aligner = "ALIGN_SUM"
      }
    }
  }
  notification_channels = [google_monitoring_notification_channel.email.id]
}
