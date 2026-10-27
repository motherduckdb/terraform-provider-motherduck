terraform {
  required_version = ">= 1.5.0"

  required_providers {
    motherduck = {
      source  = "motherduckdb/motherduck"
      version = "~> 0.3.0"
    }
  }
}

# Credentials come from the environment. MOTHERDUCK_TOKEN authorizes SQL
# resources and MOTHERDUCK_ADMIN_TOKEN authorizes organization REST resources
# such as service accounts and access tokens. Keep tokens out of configuration.
provider "motherduck" {}

resource "motherduck_database" "analytics" {
  name                    = "analytics"
  snapshot_retention_days = 7
}
