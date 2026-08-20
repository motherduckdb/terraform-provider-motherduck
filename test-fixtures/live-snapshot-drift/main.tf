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
  database_name = "tf_snapshot_drift_${local.suffix}"
  snapshot_name = "tf_snapshot_drift_named_${local.suffix}"
}

resource "motherduck_database" "source" {
  name                    = local.database_name
  snapshot_retention_days = 1
}

resource "motherduck_schema" "app" {
  database = motherduck_database.source.name
  name     = "app"
}

resource "motherduck_table" "facts" {
  database = motherduck_database.source.name
  schema   = motherduck_schema.app.name
  name     = "facts"

  columns = {
    id    = "INTEGER"
    label = "VARCHAR"
  }
}

resource "motherduck_snapshot" "named" {
  database = motherduck_database.source.name
  name     = local.snapshot_name

  depends_on = [motherduck_table.facts]
}

data "motherduck_database_snapshots" "source" {
  database_name = motherduck_database.source.name

  depends_on = [motherduck_snapshot.named]
}

output "database_name" {
  value = motherduck_database.source.name
}

output "snapshot_id" {
  value = motherduck_snapshot.named.id
}

output "snapshot_name" {
  value = motherduck_snapshot.named.name
}

output "snapshot_rows_json" {
  value     = data.motherduck_database_snapshots.source.rows_json
  sensitive = true
}
