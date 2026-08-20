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
  database_name = "tf_ducklake_${local.suffix}"
}

resource "motherduck_database" "ducklake" {
  name                    = local.database_name
  database_type           = "ducklake"
  encrypted               = true
  snapshot_retention_days = 7
}

resource "motherduck_schema" "app" {
  database = motherduck_database.ducklake.name
  name     = "app"
}

resource "motherduck_table" "facts" {
  database = motherduck_database.ducklake.name
  schema   = motherduck_schema.app.name
  name     = "facts"

  columns = {
    id    = "INTEGER"
    label = "VARCHAR"
  }
}

data "motherduck_databases" "ducklake" {
  name = motherduck_database.ducklake.name

  depends_on = [motherduck_database.ducklake]
}

output "database_name" {
  value = motherduck_database.ducklake.name
}

output "database_type" {
  value = motherduck_database.ducklake.database_type
}

output "snapshot_retention_days" {
  value = motherduck_database.ducklake.snapshot_retention_days
}

output "database_rows_json" {
  value     = data.motherduck_databases.ducklake.rows_json
  sensitive = true
}
