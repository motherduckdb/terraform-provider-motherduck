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
  database = "tf_share_modes_${local.suffix}"
  share    = "tf_share_modes_share_${local.suffix}"
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

resource "motherduck_share" "organization" {
  name            = local.share
  source_database = motherduck_database.source.name
  access          = "organization"
  visibility      = "discoverable"
  update_mode     = "manual"

  depends_on = [motherduck_table.facts]
}

data "motherduck_owned_shares" "organization" {
  name = motherduck_share.organization.name

  depends_on = [motherduck_share.organization]
}

output "share_name" {
  value = motherduck_share.organization.name
}

output "share_access" {
  value = motherduck_share.organization.access
}

output "share_visibility" {
  value = motherduck_share.organization.visibility
}

output "share_update_mode" {
  value = motherduck_share.organization.update_mode
}

output "share_rows_json" {
  value     = data.motherduck_owned_shares.organization.rows_json
  sensitive = true
}
