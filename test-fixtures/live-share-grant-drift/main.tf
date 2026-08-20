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
  database    = "tf_grant_drift_${local.suffix}"
  reader      = "tf_grant_drift_reader_${local.suffix}"
  share       = "tf_grant_drift_share_${local.suffix}"
  schema_name = "app"
}

resource "motherduck_service_account" "reader" {
  username = local.reader
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

resource "motherduck_share_grant" "reader" {
  share    = motherduck_share.source.name
  username = motherduck_service_account.reader.username
}

output "share_name" {
  value = motherduck_share.source.name
}

output "reader_username" {
  value = motherduck_service_account.reader.username
}
