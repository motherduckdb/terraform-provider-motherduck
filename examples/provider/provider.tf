terraform {
  required_providers {
    motherduck = {
      source  = "motherduckdb/motherduck"
      version = ">= 0.1.0"
    }
  }
}

provider "motherduck" {
  token       = var.motherduck_token
  admin_token = var.motherduck_admin_token
}

variable "motherduck_token" {
  type      = string
  sensitive = true
}

variable "motherduck_admin_token" {
  type      = string
  sensitive = true
}
