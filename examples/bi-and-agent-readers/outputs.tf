output "consumers" {
  description = "Non-secret metadata for each consumer."
  value = {
    for key, consumer in var.consumers : key => {
      username      = motherduck_service_account.consumer[key].username
      purpose       = consumer.purpose
      instance_size = consumer.instance_size
      flock_size    = consumer.flock_size
      relation      = "marts.reporting.daily_revenue"
    }
  }
}

output "share_name" {
  description = "Share that every consumer reads."
  value       = motherduck_share.marts.name
}

output "share_url" {
  description = "Attach this URL as marts while connected as each consumer."
  value       = motherduck_share.marts.url
  sensitive   = true
}

output "setup_tokens" {
  description = "One-hour read-write tokens for the first share attachment. Never distribute them to a tool or agent."
  value       = { for key in keys(var.consumers) : key => motherduck_access_token.setup[key].token }
  sensitive   = true
}

output "reader_tokens" {
  description = "Read-scaling tokens for each tool's or agent's secret store. Rotate before expiration."
  value       = { for key in keys(var.consumers) : key => motherduck_access_token.reader[key].token }
  sensitive   = true
}
