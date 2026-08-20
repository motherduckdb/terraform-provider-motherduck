resource "motherduck_role" "analytics_readers" {
  name = "analytics_readers"
}

resource "motherduck_role_grant" "service_account" {
  role_name    = motherduck_role.analytics_readers.name
  grantee_name = "svc_analytics_reader"
  grantee_type = "user"
}
