resource "motherduck_role" "analytics_readers" {
  name = "analytics_readers"
}

resource "motherduck_service_account" "analytics_reader" {
  username = "svc_analytics_reader"
}

resource "motherduck_role_grant" "inherit_explorer" {
  role_name    = "explorer"
  grantee_name = motherduck_role.analytics_readers.name
  grantee_type = "role"
}

resource "motherduck_role_grant" "service_account" {
  role_name    = motherduck_role.analytics_readers.name
  grantee_name = motherduck_service_account.analytics_reader.username
  grantee_type = "user"
}
