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

variable "expected_writer_username" {
  type = string
}

locals {
  suffix = replace(var.run_id, "-", "_")
}

data "motherduck_current_user" "writer" {}

resource "motherduck_database" "tenant" {
  name                    = "tf_writerpath_${local.suffix}"
  snapshot_retention_days = 1

  lifecycle {
    precondition {
      condition     = data.motherduck_current_user.writer.value == var.expected_writer_username
      error_message = "The provider SQL identity must be the writer service account so the tenant database and share are owned by the writer."
    }
  }
}

resource "motherduck_schema" "tenant" {
  database = motherduck_database.tenant.name
  name     = "app"
}

resource "motherduck_table" "facts" {
  database = motherduck_database.tenant.name
  schema   = motherduck_schema.tenant.name
  name     = "facts"

  columns = {
    tenant_id = "VARCHAR"
    amount    = "DOUBLE"
  }
}

resource "motherduck_service_account" "reader" {
  username = "tf_wreader_${local.suffix}"
}

resource "motherduck_access_token" "reader" {
  username   = motherduck_service_account.reader.username
  name       = "writer-path-smoke"
  token_type = "read_scaling"
  ttl        = 3600
}

resource "motherduck_share" "tenant" {
  name            = "tf_writerpath_share_${local.suffix}"
  source_database = motherduck_database.tenant.name
  access          = "restricted"
  visibility      = "hidden"

  depends_on = [motherduck_table.facts]
}

resource "motherduck_share_grant" "reader" {
  share    = motherduck_share.tenant.name
  username = motherduck_service_account.reader.username
}

output "current_user" {
  value = data.motherduck_current_user.writer.value
}

output "tenant_database" {
  value = motherduck_database.tenant.name
}

output "tenant_share" {
  value = motherduck_share.tenant.name
}
