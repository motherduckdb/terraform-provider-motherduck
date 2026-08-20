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

locals {
  suffix        = replace(var.run_id, "-", "_")
  database_name = "tf_view_drift_${local.suffix}"
  schema_name   = "app"
  table_name    = "facts"
  view_name     = "facts_v"
}

resource "motherduck_database" "source" {
  name                    = local.database_name
  snapshot_retention_days = 1
}

resource "motherduck_schema" "app" {
  database = motherduck_database.source.name
  name     = local.schema_name
}

resource "motherduck_table" "facts" {
  database = motherduck_database.source.name
  schema   = motherduck_schema.app.name
  name     = local.table_name

  columns = {
    id    = "INTEGER"
    label = "VARCHAR"
  }
}

resource "motherduck_view" "facts_v" {
  database = motherduck_database.source.name
  schema   = motherduck_schema.app.name
  name     = local.view_name
  query    = "SELECT id, label FROM \"${motherduck_database.source.name}\".\"${motherduck_schema.app.name}\".\"${motherduck_table.facts.name}\""

  depends_on = [motherduck_table.facts]
}

output "database_name" {
  value = motherduck_database.source.name
}

output "schema_name" {
  value = motherduck_schema.app.name
}

output "table_name" {
  value = motherduck_table.facts.name
}

output "view_name" {
  value = motherduck_view.facts_v.name
}
