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
  database = "tf_table_types_${replace(var.run_id, "-", "_")}"
}

resource "motherduck_database" "types" {
  name                    = local.database
  snapshot_retention_days = 1
}

resource "motherduck_schema" "app" {
  database = motherduck_database.types.name
  name     = "app"
}

resource "motherduck_table" "canonical" {
  database = motherduck_database.types.name
  schema   = motherduck_schema.app.name
  name     = "canonical_types"

  columns = {
    id         = "BIGINT"
    active     = "BOOLEAN"
    event_date = "DATE"
    event_ts   = "TIMESTAMP"
    amount     = "DECIMAL(18,2)"
    ratio      = "DOUBLE"
    label      = "VARCHAR"
  }
}

output "database_name" {
  value = motherduck_database.types.name
}

output "table_id" {
  value = motherduck_table.canonical.id
}
