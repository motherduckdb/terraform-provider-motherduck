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
  suffix   = replace(var.run_id, "-", "_")
  username = "tf_token_matrix_${local.suffix}"
}

resource "motherduck_service_account" "matrix" {
  username = local.username
}

resource "motherduck_duckling_config" "matrix" {
  username = motherduck_service_account.matrix.username

  read_write_instance_size   = "pulse"
  read_scaling_instance_size = "pulse"
  read_scaling_flock_size    = 2
}

resource "motherduck_access_token" "default" {
  username = motherduck_service_account.matrix.username
  name     = "terraform-default"

  depends_on = [motherduck_duckling_config.matrix]
}

resource "motherduck_access_token" "read_scaling" {
  username   = motherduck_service_account.matrix.username
  name       = "terraform-read-scaling"
  token_type = "read_scaling"
  ttl        = 3600

  depends_on = [motherduck_duckling_config.matrix]
}

data "motherduck_user_tokens" "matrix" {
  username = motherduck_service_account.matrix.username

  depends_on = [
    motherduck_access_token.default,
    motherduck_access_token.read_scaling,
  ]
}

output "username" {
  value = motherduck_service_account.matrix.username
}

output "default_token_type" {
  value = motherduck_access_token.default.token_type
}

output "read_scaling_token_type" {
  value = motherduck_access_token.read_scaling.token_type
}

output "tokens_json" {
  value     = data.motherduck_user_tokens.matrix.tokens_json
  sensitive = true
}
