# =============================================================================
# IAM bundle: the roles needed to use Gemini Code Assist Standard / Enterprise
# are granted ONCE to a Google group on the Gemini project. The portal grants
# credentials by adding the user to the group (Cloud Identity API) — one
# synchronous call instead of asynchronous per-user IAM policy edits.
# =============================================================================

resource "google_cloud_identity_group" "gca_users" {
  count                = var.create_gca_users_group ? 1 : 0
  provider             = google-beta
  display_name         = "Gemini Code Assist users"
  description          = "Managed by the GCA license portal — do not edit by hand."
  parent               = "customers/${var.customer_id}"
  initial_group_config = "WITH_INITIAL_OWNER"
  group_key {
    id = var.gca_users_group_email
  }
  labels = {
    "cloudidentity.googleapis.com/groups.discussion_forum" = ""
  }
}

# The portal service accounts manage the group membership.
resource "google_cloud_identity_group_membership" "portal_managers" {
  for_each = var.create_gca_users_group ? local.runtime_sas : {}
  provider = google-beta
  group    = google_cloud_identity_group.gca_users[0].id
  preferred_member_key {
    id = each.value
  }
  roles {
    name = "MEMBER"
  }
  roles {
    name = "MANAGER"
  }
}

resource "google_project_iam_member" "gca_bundle" {
  for_each = toset([
    "roles/cloudaicompanion.user",             # Gemini for Google Cloud User
    "roles/serviceusage.serviceUsageConsumer", # Service Usage Consumer
  ])
  project = var.gca_project_id
  role    = each.value
  member  = "group:${var.gca_users_group_email}"
}
