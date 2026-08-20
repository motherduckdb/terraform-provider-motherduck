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
  suffix   = replace(var.run_id, "-", "_")
  database = "tf_share_option_drift_${local.suffix}"
  share    = "tf_share_option_drift_share_${local.suffix}"
}

resource "motherduck_database" "source" {
  name                    = local.database
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

resource "motherduck_share" "source" {
  name            = local.share
  source_database = motherduck_database.source.name
  access          = "restricted"
  visibility      = "hidden"
  update_mode     = "automatic"

  depends_on = [motherduck_table.facts]
}

output "database_name" {
  value = motherduck_database.source.name
}

output "share_name" {
  value = motherduck_share.source.name
}

output "share_access" {
  value = motherduck_share.source.access
}

output "share_visibility" {
  value = motherduck_share.source.visibility
}

output "share_update_mode" {
  value = motherduck_share.source.update_mode
}
