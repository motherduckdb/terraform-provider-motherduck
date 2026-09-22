resource "motherduck_service_account" "writer" {
  username = var.writer_username

  lifecycle {
    precondition {
      condition     = !var.retire_legacy_writer_token || length(var.writer_token_generations) > 0
      error_message = "Keep at least one writer_token_generations entry when retiring the legacy writer token."
    }
  }
}

resource "motherduck_access_token" "writer_legacy" {
  count = var.retire_legacy_writer_token ? 0 : 1

  username   = motherduck_service_account.writer.username
  name       = var.writer_token_name
  token_type = "read_write"
  ttl        = var.writer_token_ttl_seconds
}

moved {
  from = motherduck_access_token.writer
  to   = motherduck_access_token.writer_legacy[0]
}

resource "motherduck_access_token" "writer_rotation" {
  for_each = var.writer_token_generations

  username   = motherduck_service_account.writer.username
  name       = "${var.writer_token_name}-${each.value}"
  token_type = "read_write"
  ttl        = var.writer_token_ttl_seconds
}
