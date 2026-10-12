# Copy to <env>.tfvars (git-ignored) and adapt.
project_id             = "my-gca-portal"
region                 = "europe-west1"
org_id                 = "123456789012"
billing_account_id     = "000000-000000-000000"
gca_project_id         = "my-gemini-project"
gca_users_group_email  = "gca-users@example.com"
create_gca_users_group = false
portal_users           = ["domain:example.com"]
image                  = "europe-west1-docker.pkg.dev/my-gca-portal/gcalm/gcalm:1.0.0"
directory_url          = "https://directory.example.com/api"
mail_from              = "gca-portal@example.com"
tracker_mode           = "jira"
tracker_env = {
  JIRA_URL     = "https://example.atlassian.net"
  JIRA_USER    = "gca-portal-bot@example.com"
  JIRA_PROJECT = "GCA"
}
alert_email = "platform-team@example.com"
