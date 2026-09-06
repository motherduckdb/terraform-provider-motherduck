output "usernames" {
  description = "Environment writer usernames. BI uses the matching writer identity with a separate read-scaling token."
  value       = { for key, account in motherduck_service_account.environment : key => account.username }
}

output "tokens" {
  description = "Creation-only credentials. Transfer securely into separate environment secret stores. Tokens do not expire; revoke them when no longer needed."
  value       = { for key, token in motherduck_access_token.environment : key => token.token }
  sensitive   = true
}
