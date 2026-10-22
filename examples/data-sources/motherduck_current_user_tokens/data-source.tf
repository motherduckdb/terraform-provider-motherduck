data "motherduck_current_user_tokens" "current" {}

output "current_user_token_names" {
  value = [for token in data.motherduck_current_user_tokens.current.rows : token.token_name]
}
