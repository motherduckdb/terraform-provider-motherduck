output "writer_username" {
  description = "Writer service account username."
  value       = motherduck_service_account.writer.username
}

output "writer_token" {
  description = "Generated writer token. Store this in a secret manager. Downstream data-plane Terraform uses it as MOTHERDUCK_TOKEN."
  value       = try(motherduck_access_token.writer_legacy[0].token, null)
  sensitive   = true
}

output "writer_rotation_tokens" {
  description = "Overlapping read-write token generations keyed by generation. Store these in a secret manager before retiring the legacy writer token."
  value = {
    for generation, token in motherduck_access_token.writer_rotation :
    generation => token.token
  }
  sensitive = true
}
