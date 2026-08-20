terraform {
  required_version = ">= 1.5.0"

  required_providers {
    motherduck = {
      source  = "motherduckdb/motherduck"
      version = "= 0.1.0"
    }
  }
}

provider "motherduck" {}

variable "run_id" {
  type = string
}

variable "tenants" {
  type = map(object({
    display_name            = string
    slug                    = optional(string)
    snapshot_retention_days = optional(number, 1)
  }))
}

locals {
  suffix = replace(var.run_id, "-", "_")

  tenant_slugs = {
    for tenant_id, tenant in var.tenants :
    tenant_id => replace(lower(coalesce(tenant.slug, tenant_id)), "/[^a-z0-9_]/", "_")
  }
}

resource "motherduck_database" "tenant" {
  for_each = var.tenants

  name                    = "tf_blueprint_${local.suffix}_${local.tenant_slugs[each.key]}"
  snapshot_retention_days = each.value.snapshot_retention_days
}

resource "motherduck_schema" "tenant" {
  for_each = var.tenants

  database = motherduck_database.tenant[each.key].name
  name     = "app"
}

resource "motherduck_table" "facts" {
  for_each = var.tenants

  database = motherduck_database.tenant[each.key].name
  schema   = motherduck_schema.tenant[each.key].name
  name     = "facts"

  columns = {
    tenant_id = "VARCHAR"
    event_id  = "INTEGER"
    amount    = "DOUBLE"
  }
}

resource "motherduck_view" "tenant_summary" {
  for_each = var.tenants

  database = motherduck_database.tenant[each.key].name
  schema   = motherduck_schema.tenant[each.key].name
  name     = "tenant_summary"
  query    = "SELECT tenant_id, count(*) AS event_count, sum(amount) AS total_amount FROM ${motherduck_schema.tenant[each.key].name}.${motherduck_table.facts[each.key].name} GROUP BY tenant_id"
}

resource "motherduck_share" "tenant" {
  for_each = var.tenants

  name            = "tf_blueprint_share_${local.suffix}_${local.tenant_slugs[each.key]}"
  source_database = motherduck_database.tenant[each.key].name
  access          = "restricted"
  visibility      = "hidden"
  update_mode     = "automatic"

  depends_on = [motherduck_view.tenant_summary]
}

output "tenant_databases" {
  value = {
    for tenant_id, database in motherduck_database.tenant :
    tenant_id => database.name
  }
}

output "tenant_shares" {
  value = {
    for tenant_id, share in motherduck_share.tenant :
    tenant_id => share.name
  }
}
