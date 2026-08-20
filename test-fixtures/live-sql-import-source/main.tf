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
  database_name = "tf_import_${local.suffix}"
  schema_name   = "app"
  secret_name   = "tf_import_s3_${local.suffix}"
  share_name    = "tf_import_share_${local.suffix}"
  snapshot_name = "tf_import_snapshot_${local.suffix}"
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
  name     = "facts"

  columns = {
    id    = "INTEGER"
    label = "VARCHAR"
  }
}

resource "motherduck_view" "facts_v" {
  database = motherduck_database.source.name
  schema   = motherduck_schema.app.name
  name     = "facts_v"
  query    = <<-SQL
    SELECT
      id,
      label
    FROM "${motherduck_database.source.name}"."${motherduck_schema.app.name}"."${motherduck_table.facts.name}"
  SQL

  depends_on = [motherduck_table.facts]
}

resource "motherduck_snapshot" "imported" {
  database = motherduck_database.source.name
  name     = local.snapshot_name

  depends_on = [
    motherduck_table.facts,
    motherduck_view.facts_v,
  ]
}

resource "motherduck_share" "imported" {
  name            = local.share_name
  source_database = motherduck_database.source.name
  access          = "restricted"
  visibility      = "hidden"
  update_mode     = "automatic"

  depends_on = [
    motherduck_table.facts,
    motherduck_view.facts_v,
  ]
}

resource "motherduck_secret" "import_s3" {
  name = local.secret_name
  type = "s3"

  params = {
    key_id = "terraform-import-key"
    region = "us-east-1"
    scope  = "s3://terraform-provider-motherduck/import/${var.run_id}/"
    secret = "terraform-import-secret"
  }
}

output "database_name" {
  value = motherduck_database.source.name
}

output "schema_name" {
  value = motherduck_schema.app.name
}

output "view_name" {
  value = motherduck_view.facts_v.name
}

output "table_name" {
  value = motherduck_table.facts.name
}

output "snapshot_name" {
  value = motherduck_snapshot.imported.name
}

output "share_name" {
  value = motherduck_share.imported.name
}

output "secret_name" {
  value = motherduck_secret.import_s3.name
}
