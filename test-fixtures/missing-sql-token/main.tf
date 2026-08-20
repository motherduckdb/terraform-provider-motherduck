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

data "motherduck_current_user" "current" {}

output "current_user" {
  value = data.motherduck_current_user.current.value
}
