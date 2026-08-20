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

data "motherduck_active_accounts" "current" {}

output "accounts_json" {
  value = data.motherduck_active_accounts.current.accounts_json
}
