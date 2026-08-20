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

variable "run_id" {
  type = string
}

locals {
  suffix = replace(var.run_id, "-", "_")
}

resource "motherduck_service_account" "writer" {
  username = "tf_writer_${local.suffix}"
}

resource "motherduck_access_token" "writer" {
  username   = motherduck_service_account.writer.username
  name       = "writer-path-smoke"
  token_type = "read_write"
  ttl        = 3600
}

output "writer_username" {
  value = motherduck_service_account.writer.username
}

output "writer_token" {
  value     = motherduck_access_token.writer.token
  sensitive = true
}
