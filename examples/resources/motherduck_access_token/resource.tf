resource "motherduck_service_account" "app" {
  username = "analytics_app"
}

resource "motherduck_access_token" "app" {
  username    = motherduck_service_account.app.username
  name        = "nightly_ingest_v1"
  description = "Nightly ingestion pipeline"
  token_type  = "read_write"
  ttl         = 2592000
}

# The token secret is available only after creation. Pass it to a secret
# manager rather than printing it.
output "app_token" {
  value     = motherduck_access_token.app.token
  sensitive = true
}
