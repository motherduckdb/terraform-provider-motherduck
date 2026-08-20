resource "motherduck_service_account" "writer" {
  username = var.writer_username
}

resource "motherduck_access_token" "writer" {
  username   = motherduck_service_account.writer.username
  name       = var.writer_token_name
  token_type = "read_write"
  ttl        = var.writer_token_ttl_seconds
}
