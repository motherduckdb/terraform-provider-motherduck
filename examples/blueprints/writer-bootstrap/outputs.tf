output "writer_username" {
  description = "Writer service account username."
  value       = motherduck_service_account.writer.username
}

output "writer_token" {
  description = "Generated writer token. Store this in a secret manager; downstream data-plane Terraform uses it as MOTHERDUCK_TOKEN."
  value       = motherduck_access_token.writer.token
  sensitive   = true
}
