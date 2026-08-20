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
  database_name = "tf_database_drop_objects_${local.suffix}"
}

resource "motherduck_database" "source" {
  name                    = local.database_name
  snapshot_retention_days = 1
}

output "database_name" {
  value = motherduck_database.source.name
}
