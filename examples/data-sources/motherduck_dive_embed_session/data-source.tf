# Deprecated. This data source stores the session credential in Terraform
# state. Use the motherduck_dive_embed_session ephemeral resource instead.
data "motherduck_dive_embed_session" "reader" {
  dive_id      = "11111111-1111-4111-8111-111111111111"
  username     = "analytics_reader"
  session_name = "dashboard"
}
