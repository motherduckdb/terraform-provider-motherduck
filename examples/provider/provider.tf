terraform {
  required_version = ">= 1.5.0"

  required_providers {
    motherduck = {
      source  = "motherduckdb/motherduck"
      version = "~> 0.2.2"
    }
  }
}

provider "motherduck" {}

resource "motherduck_database" "analytics" {
  name                    = "analytics"
  snapshot_retention_days = 7
}
