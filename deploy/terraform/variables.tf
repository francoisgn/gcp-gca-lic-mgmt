variable "project_id" {
  description = "Project hosting the portal (Cloud Run, Cloud SQL, Scheduler)."
  type        = string
}

variable "region" {
  description = "Region of Cloud Run, Cloud SQL and Cloud Scheduler."
  type        = string
  default     = "europe-west1"
}

variable "org_id" {
  description = "Organization ID (numeric), used for the license manager custom role."
  type        = string
}

variable "billing_account_id" {
  description = "Billing account holding the Gemini Code Assist orders (XXXXXX-XXXXXX-XXXXXX)."
  type        = string
}

variable "gca_project_id" {
  description = "Project where the Gemini for Google Cloud API is enabled and that users select in their IDE."
  type        = string
}

variable "gca_users_group_email" {
  description = "Google group carrying the IAM bundle (created if create_gca_users_group = true)."
  type        = string
}

variable "create_gca_users_group" {
  description = "Create the IAM bundle group with Cloud Identity (needs customer_id)."
  type        = bool
  default     = false
}

variable "customer_id" {
  description = "Cloud Identity customer ID (C0xxxxxxx), only when create_gca_users_group = true."
  type        = string
  default     = ""
}

variable "portal_users" {
  description = "Principals allowed through IAP (e.g. [\"domain:example.com\"] or a group)."
  type        = list(string)
}

variable "image" {
  description = "Container image (Artifact Registry) to deploy."
  type        = string
}

variable "base_url" {
  description = "Public URL of the portal (used in e-mails). Defaults to the Cloud Run URL."
  type        = string
  default     = ""
}

variable "db_tier" {
  description = "Cloud SQL machine tier."
  type        = string
  default     = "db-custom-1-3840"
}

variable "time_zone" {
  description = "Time zone of the KPIs and of the scheduler."
  type        = string
  default     = "Europe/Paris"
}

variable "directory_url" {
  description = "Base URL of the enterprise directory API."
  type        = string
}

variable "mail_from" {
  description = "Sender of the portal e-mails."
  type        = string
}

variable "tracker_mode" {
  description = "Feedback tracker: jira, gitlab or log."
  type        = string
  default     = "jira"
}

variable "tracker_env" {
  description = "Non secret tracker settings (JIRA_URL, JIRA_USER, JIRA_PROJECT or GITLAB_URL, GITLAB_PROJECT)."
  type        = map(string)
  default     = {}
}

variable "alert_email" {
  description = "E-mail notified by the alerting policies."
  type        = string
}
