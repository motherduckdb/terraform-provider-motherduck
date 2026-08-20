resource "motherduck_service_account" "app" {
  username = "analytics_app"
}

resource "motherduck_access_token" "app" {
  username   = motherduck_service_account.app.username
  name       = "terraform-managed"
  token_type = "read_write"
  ttl        = 2592000
}

