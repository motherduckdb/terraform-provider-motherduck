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
  database    = "tf_canonical_${local.suffix}"
  schema      = "app"
  table       = "facts"
  share       = "tf_canonical_share_${local.suffix}"
  secret_name = "tf_canonical_secret_${local.suffix}"
}

resource "motherduck_database" "canonical" {
  name          = local.database
  database_type = "default"
}

resource "motherduck_schema" "app" {
  database = motherduck_database.canonical.name
  name     = local.schema
}

resource "motherduck_table" "facts" {
  database = motherduck_database.canonical.name
  schema   = motherduck_schema.app.name
  name     = local.table

  columns = {
    id = "BIGINT"
  }
}

resource "motherduck_share" "canonical" {
  name            = local.share
  source_database = motherduck_database.canonical.name
  access          = "restricted"
  visibility      = "hidden"
  update_mode     = "automatic"

  depends_on = [motherduck_table.facts]
}

resource "motherduck_secret" "canonical" {
  name = local.secret_name
  type = "s3"

  params = {
    key_id = "terraform-canonical-key"
    region = "us-east-1"
    scope  = "s3://terraform-provider-motherduck/canonical/${var.run_id}/"
    secret = "terraform-canonical-secret"
  }
}

output "database_type" {
  value = motherduck_database.canonical.database_type
}

output "share_access" {
  value = motherduck_share.canonical.access
}

output "share_visibility" {
  value = motherduck_share.canonical.visibility
}

output "share_update_mode" {
  value = motherduck_share.canonical.update_mode
}

output "secret_type" {
  value = motherduck_secret.canonical.type
}
