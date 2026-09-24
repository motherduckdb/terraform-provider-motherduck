data "motherduck_user_tokens" "writer_rotation_preflight" {
  count    = var.retire_legacy_writer_token ? 1 : 0
  username = var.writer_username
}

resource "motherduck_service_account" "writer" {
  username = var.writer_username

  lifecycle {
    precondition {
      condition = var.retire_legacy_writer_token ? anytrue([
        for token in nonsensitive(data.motherduck_user_tokens.writer_rotation_preflight[0].tokens) :
        token.token_type == "read_write" && contains([
          for generation in var.writer_token_generations : "${var.writer_token_name}-${generation}"
        ], token.name)
      ]) : true
      error_message = "Create and verify a writer rotation token in an earlier apply before retiring the legacy token."
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
