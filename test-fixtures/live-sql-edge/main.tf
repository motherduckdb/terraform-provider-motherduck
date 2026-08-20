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

variable "snapshot_name" {
  type = string
}

variable "retention_days" {
  type = number
}

variable "secret_scope" {
  type = string
}

locals {
  suffix        = replace(var.run_id, "-", "_")
  database_name = "tf_edge_${local.suffix}"
  secret_name   = "tf_edge_s3_${local.suffix}"
}

resource "motherduck_database" "edge" {
  name                    = local.database_name
  snapshot_retention_days = var.retention_days
}

resource "motherduck_schema" "app" {
  database = motherduck_database.edge.name
  name     = "app"
}

resource "motherduck_table" "facts" {
  database = motherduck_database.edge.name
  schema   = motherduck_schema.app.name
  name     = "facts"

  columns = {
    id         = "INTEGER"
    label      = "VARCHAR"
    created_at = "TIMESTAMP"
  }
}

resource "motherduck_view" "facts_v" {
  database = motherduck_database.edge.name
  schema   = motherduck_schema.app.name
  name     = "facts_v"
  query    = <<-SQL
    SELECT
      id,
      label,
      created_at
    FROM "${motherduck_database.edge.name}"."${motherduck_schema.app.name}"."${motherduck_table.facts.name}"
  SQL

  depends_on = [motherduck_table.facts]
}

resource "motherduck_snapshot" "edge" {
  database = motherduck_database.edge.name
  name     = var.snapshot_name

  depends_on = [
    motherduck_table.facts,
    motherduck_view.facts_v,
  ]
}

resource "motherduck_secret" "edge_s3" {
  name = local.secret_name
  type = "s3"

  params = {
    key_id = "terraform-test-key"
    region = "us-east-1"
    scope  = var.secret_scope
    secret = "terraform-test-secret"
  }
}

data "motherduck_databases" "edge" {
  name = motherduck_database.edge.name

  depends_on = [motherduck_database.edge]
}

data "motherduck_database_snapshots" "edge" {
  database_name = motherduck_database.edge.name

  depends_on = [motherduck_snapshot.edge]
}

data "motherduck_secrets" "edge" {
  name = motherduck_secret.edge_s3.name

  depends_on = [motherduck_secret.edge_s3]
}

output "database_name" {
  value = motherduck_database.edge.name
}

output "snapshot_name" {
  value = motherduck_snapshot.edge.name
}

output "secret_name" {
  value = motherduck_secret.edge_s3.name
}

output "database_rows_json" {
  value     = data.motherduck_databases.edge.rows_json
  sensitive = true
}

output "snapshot_rows_json" {
  value     = data.motherduck_database_snapshots.edge.rows_json
  sensitive = true
}

output "secret_rows_json" {
  value     = data.motherduck_secrets.edge.rows_json
  sensitive = true
}
