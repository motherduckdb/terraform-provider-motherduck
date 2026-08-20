terraform {
  required_version = ">= 1.5.0"

  required_providers {
    motherduck = {
      source  = "motherduckdb/motherduck"
      version = "= 0.1.0"
    }
  }
}

variable "database_name" {
  type = string
}

variable "attach_mode" {
  type    = string
  default = null
}

provider "motherduck" {
  database          = var.database_name
  attach_mode       = var.attach_mode
  custom_user_agent = "terraform-provider-motherduck-live-provider-config"
}

data "motherduck_attached_databases" "current" {}

data "motherduck_current_user" "current" {}

output "attached_rows_json" {
  value     = data.motherduck_attached_databases.current.rows_json
  sensitive = true
}

output "current_user_present" {
  value = length(data.motherduck_current_user.current.value) > 0
}

output "database_name" {
  value = var.database_name
}
