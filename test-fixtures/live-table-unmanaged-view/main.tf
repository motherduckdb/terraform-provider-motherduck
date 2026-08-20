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

variable "include_label" {
  type = bool
}

locals {
  suffix        = replace(var.run_id, "-", "_")
  database_name = "tf_unmanaged_view_${local.suffix}"
  columns = var.include_label ? {
    id    = "INTEGER"
    label = "VARCHAR"
    } : {
    id     = "INTEGER"
    amount = "DOUBLE"
  }
}

resource "motherduck_database" "source" {
  name                    = local.database_name
  snapshot_retention_days = 1
}

resource "motherduck_schema" "app" {
  database          = motherduck_database.source.name
  name              = "app"
  cascade_on_delete = true
}

resource "motherduck_table" "facts" {
  database = motherduck_database.source.name
  schema   = motherduck_schema.app.name
  name     = "facts"
  columns  = local.columns
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
