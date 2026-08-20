output "tenants" {
  description = "Per-tenant summary keyed by tenant id: display name, database, share, and reader username."
  value = {
    for tenant_id in keys(motherduck_database.tenant) :
    tenant_id => {
      display_name    = var.tenants[tenant_id].display_name
      database        = motherduck_database.tenant[tenant_id].name
      share           = motherduck_share.tenant[tenant_id].name
      reader_username = motherduck_service_account.reader[tenant_id].username
    }
  }
}

output "tenant_databases" {
  description = "Tenant database names keyed by tenant id."
  value = {
    for tenant_id, database in motherduck_database.tenant :
    tenant_id => database.name
  }
}

output "tenant_shares" {
  description = "Tenant share names keyed by tenant id."
  value = {
    for tenant_id, share in motherduck_share.tenant :
    tenant_id => share.name
  }
}

output "reader_usernames" {
  description = "Reader service account usernames keyed by tenant id."
  value = {
    for tenant_id, account in motherduck_service_account.reader :
    tenant_id => account.username
  }
}

output "reader_tokens" {
  description = "Generated reader tokens keyed by tenant id. Store these in a secret manager."
  value = {
    for tenant_id, token in motherduck_access_token.reader :
    tenant_id => token.token
  }
  sensitive = true
}
