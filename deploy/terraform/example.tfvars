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
alert_email            = "platform-team@example.com"
# Directory, GitLab and SMTP endpoints: config/settings.yaml (versioned).
# Keyless CI/CD from GitLab (optional): project allowed to deploy, instance URL.
gitlab_project_path = "platform/gca-license-portal"
gitlab_url          = "https://gitlab.example.com"
