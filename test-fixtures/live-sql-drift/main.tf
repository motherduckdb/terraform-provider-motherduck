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
  suffix      = replace(var.run_id, "-", "_")
  database    = "tf_drift_${local.suffix}"
  schema_name = "app"
  table_name  = "facts"
  share_name  = "tf_drift_share_${local.suffix}"
  secret_name = "tf_drift_s3_${local.suffix}"
}

resource "motherduck_database" "source" {
  name                    = local.database
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

resource "motherduck_share" "source" {
  name            = local.share_name
  source_database = motherduck_database.source.name
  access          = "restricted"
  visibility      = "hidden"
  update_mode     = "automatic"

  depends_on = [motherduck_table.facts]
}

resource "motherduck_secret" "source" {
  name = local.secret_name
  type = "s3"

  params = {
    key_id = "terraform-drift-key"
    region = "us-east-1"
    scope  = "s3://terraform-provider-motherduck/drift/${var.run_id}/"
    secret = "terraform-drift-secret"
  }
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

output "share_name" {
  value = motherduck_share.source.name
}

output "secret_name" {
  value = motherduck_secret.source.name
}
