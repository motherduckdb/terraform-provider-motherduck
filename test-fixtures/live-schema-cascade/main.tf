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

variable "schema_cascade_on_delete" {
  type = bool
}

locals {
  suffix        = replace(var.run_id, "-", "_")
  database_name = "tf_schema_cascade_${local.suffix}"
  schema_name   = "app"
}

resource "motherduck_database" "source" {
  name                    = local.database_name
  snapshot_retention_days = 1
}

resource "motherduck_schema" "app" {
  database          = motherduck_database.source.name
  name              = local.schema_name
  cascade_on_delete = var.schema_cascade_on_delete
}

output "database_name" {
  value = motherduck_database.source.name
}

output "schema_name" {
  value = motherduck_schema.app.name
}
