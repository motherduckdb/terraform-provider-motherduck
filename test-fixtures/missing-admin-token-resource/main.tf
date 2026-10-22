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

resource "motherduck_service_account" "offline_plan" {
  username = "tf_missing_admin_token_offline_plan"
}
