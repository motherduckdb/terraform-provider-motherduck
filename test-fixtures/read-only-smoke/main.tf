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

variable "enable_rest" {
  type    = bool
  default = true
}

data "motherduck_current_user" "current" {}

data "motherduck_active_accounts" "current" {
  count = var.enable_rest ? 1 : 0
}

locals {
  active_accounts = var.enable_rest ? jsondecode(data.motherduck_active_accounts.current[0].accounts_json) : []
}

output "current_user_present" {
  value = length(data.motherduck_current_user.current.value) > 0
}

output "rest_admin_checked" {
  value = var.enable_rest
}

output "active_account_count" {
  value     = length(local.active_accounts)
  sensitive = true
}
