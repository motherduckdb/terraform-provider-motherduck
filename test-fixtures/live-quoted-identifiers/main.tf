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
  database_name = "tf qident ${local.suffix}"
  schema_name   = "app schema"
  table_name    = "facts table"
  view_name     = "facts view"
  share_name    = "tf qident share ${local.suffix}"
  secret_name   = "tf qident secret ${local.suffix}"
  snapshot_name = "tf qident snapshot ${local.suffix}"
}

resource "motherduck_database" "quoted" {
  name                    = local.database_name
  snapshot_retention_days = 1
}

resource "motherduck_schema" "quoted" {
  database = motherduck_database.quoted.name
  name     = local.schema_name
}

resource "motherduck_table" "quoted" {
  database = motherduck_database.quoted.name
  schema   = motherduck_schema.quoted.name
  name     = local.table_name

  columns = {
    "id value"         = "INTEGER"
    "label \"quoted\"" = "VARCHAR"
  }
}

resource "motherduck_view" "quoted" {
  database = motherduck_database.quoted.name
  schema   = motherduck_schema.quoted.name
  name     = local.view_name
  query    = <<-SQL
    SELECT
      "id value",
      "label ""quoted"""
    FROM "${motherduck_database.quoted.name}"."${motherduck_schema.quoted.name}"."${motherduck_table.quoted.name}"
  SQL

  depends_on = [motherduck_table.quoted]
}

resource "motherduck_share" "quoted" {
  name            = local.share_name
  source_database = motherduck_database.quoted.name
  access          = "restricted"
  visibility      = "hidden"
  update_mode     = "automatic"

  depends_on = [motherduck_view.quoted]
}

resource "motherduck_snapshot" "quoted" {
  database = motherduck_database.quoted.name
  name     = local.snapshot_name

  depends_on = [motherduck_view.quoted]
}

resource "motherduck_secret" "quoted" {
  name = local.secret_name
  type = "s3"

  params = {
    key_id = "terraform-qident-key"
    region = "us-east-1"
    scope  = "s3://terraform-provider-motherduck/quoted-identifiers/${var.run_id}/"
    secret = "terraform-qident-secret"
  }
}

data "motherduck_databases" "quoted" {
  name = motherduck_database.quoted.name

  depends_on = [motherduck_database.quoted]
}

data "motherduck_owned_shares" "quoted" {
  name = motherduck_share.quoted.name

  depends_on = [motherduck_share.quoted]
}

data "motherduck_database_snapshots" "quoted" {
  database_name = motherduck_database.quoted.name

  depends_on = [motherduck_snapshot.quoted]
}

data "motherduck_secrets" "quoted" {
  name = motherduck_secret.quoted.name

  depends_on = [motherduck_secret.quoted]
}

output "database_name" {
  value = motherduck_database.quoted.name
}

output "schema_id" {
  value = motherduck_schema.quoted.id
}

output "table_id" {
  value = motherduck_table.quoted.id
}

output "view_id" {
  value = motherduck_view.quoted.id
}

output "share_name" {
  value = motherduck_share.quoted.name
}

output "snapshot_name" {
  value = motherduck_snapshot.quoted.name
}

output "secret_name" {
  value = motherduck_secret.quoted.name
}

output "database_rows_json" {
  value     = data.motherduck_databases.quoted.rows_json
  sensitive = true
}

output "share_rows_json" {
  value     = data.motherduck_owned_shares.quoted.rows_json
  sensitive = true
}

output "snapshot_rows_json" {
  value     = data.motherduck_database_snapshots.quoted.rows_json
  sensitive = true
}

output "secret_rows_json" {
  value     = data.motherduck_secrets.quoted.rows_json
  sensitive = true
}
