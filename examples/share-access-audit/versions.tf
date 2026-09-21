terraform {
  required_version = ">= 1.5.0"

  required_providers {
    motherduck = {
      source  = "motherduckdb/motherduck"
      version = ">= 0.2.9"
    }
  }
}

# Use a source build from main until a release includes motherduck_share_grants.
provider "motherduck" {}
