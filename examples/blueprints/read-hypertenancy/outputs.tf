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
  description = "Legacy generated reader tokens keyed by tenant id. Store these in a secret manager and retire them only after an overlapping replacement is verified."
  value = {
    for tenant_id, token in motherduck_access_token.reader :
    tenant_id => token.token
  }
  sensitive = true
}

output "share_urls" {
  description = "Sensitive restricted share URLs keyed by tenant id. Attach each share once with its setup token."
  value = {
    for tenant_id, share in motherduck_share.tenant :
    tenant_id => share.url
  }
  sensitive = true
}

output "reader_setup_tokens" {
  description = "One-hour read-write tokens keyed by tenant id for the initial share attachment."
  value = {
    for tenant_id, token in motherduck_access_token.reader_setup :
    tenant_id => token.token
  }
  sensitive = true
}

output "reader_rotation_tokens" {
  description = "Overlapping read-scaling token generations keyed by tenant_id/generation."
  value = {
    for key, token in motherduck_access_token.reader_rotation :
    key => token.token
  }
  sensitive = true
}
