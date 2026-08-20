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

variable "database_name" {
  type = string
}

resource "motherduck_database" "imported" {
  name                    = var.database_name
  database_type           = "ducklake"
  snapshot_retention_days = 7
}
