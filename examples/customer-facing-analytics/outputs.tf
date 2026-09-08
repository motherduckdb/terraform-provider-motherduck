output "tenants" {
  description = "Non-secret routing metadata. The application authenticates the tenant before choosing its credential."
  value = {
    for id in var.tenants : id => {
      database        = motherduck_database.tenant[id].name
      share           = motherduck_share.tenant[id].name
      reader_username = motherduck_service_account.reader[id].username
      reader_relation = "reporting.app.daily_usage"
    }
  }
}

output "share_urls" {
  description = "Attach each URL as reporting while connected as its corresponding reader."
  value       = { for id in var.tenants : id => motherduck_share.tenant[id].url }
  sensitive   = true
}

output "reader_setup_tokens" {
  description = "One-hour tokens for account initialization and first share attachment. Never distribute to the application."
  value       = { for id in var.tenants : id => motherduck_access_token.reader_setup[id].token }
  sensitive   = true
}

output "reader_tokens" {
  description = "Thirty-day read-scaling tokens for the backend secret store. Rotate before expiration."
  value       = { for id in var.tenants : id => motherduck_access_token.reader[id].token }
  sensitive   = true
}
