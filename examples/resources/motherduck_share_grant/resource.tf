resource "motherduck_share_grant" "reader" {
  share        = "analytics_share"
  username     = "analytics_readers"
  grantee_type = "role"
}
