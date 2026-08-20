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
  suffix         = replace(var.run_id, "-", "_")
  default_name   = "tf_db_options_default_${local.suffix}"
  transient_name = "tf_db_options_transient_${local.suffix}"
}

resource "motherduck_database" "default_explicit" {
  name                    = local.default_name
  database_type           = "default"
  snapshot_retention_days = 1
}

resource "motherduck_database" "transient" {
  name      = local.transient_name
  transient = true
}

data "motherduck_databases" "default_explicit" {
  name = motherduck_database.default_explicit.name

  depends_on = [motherduck_database.default_explicit]
}

data "motherduck_databases" "transient" {
  name = motherduck_database.transient.name

  depends_on = [motherduck_database.transient]
}

locals {
  default_rows   = nonsensitive(jsondecode(data.motherduck_databases.default_explicit.rows_json))
  transient_rows = nonsensitive(jsondecode(data.motherduck_databases.transient.rows_json))
}

output "default_database_type" {
  value = motherduck_database.default_explicit.database_type
}

output "default_database_rows_json" {
  value     = data.motherduck_databases.default_explicit.rows_json
  sensitive = true
}

output "transient_database_type" {
  value = motherduck_database.transient.database_type
}

output "transient_database_rows_json" {
  value     = data.motherduck_databases.transient.rows_json
  sensitive = true
}

output "transient_database_is_transient" {
  value = motherduck_database.transient.transient
}
