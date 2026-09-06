output "usernames" {
  description = "Environment writer/BI usernames; pass only the relevant BI username to a warehouse root."
  value       = { for key, account in motherduck_service_account.environment : key => account.username }
}

output "tokens" {
  description = "Creation-only credentials. Transfer securely into separate environment secret stores. Tokens expire after 30 days."
  value       = { for key, token in motherduck_access_token.environment : key => token.token }
  sensitive   = true
}
