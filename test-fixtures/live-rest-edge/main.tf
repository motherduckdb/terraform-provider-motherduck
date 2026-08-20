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

variable "token_name" {
  type = string
}

variable "token_ttl" {
  type = number
}

variable "read_write_cooldown_seconds" {
  type = number
}

variable "read_write_instance_size" {
  type = string
}

variable "read_scaling_instance_size" {
  type = string
}

variable "read_scaling_flock_size" {
  type = number
}

variable "read_scaling_cooldown_seconds" {
  type = number
}

locals {
  suffix   = replace(var.run_id, "-", "_")
  username = "tf_rest_edge_${local.suffix}"
}

resource "motherduck_service_account" "edge" {
  username = local.username
}

resource "motherduck_duckling_config" "edge" {
  username = motherduck_service_account.edge.username

  read_write_instance_size      = var.read_write_instance_size
  read_write_cooldown_seconds   = var.read_write_cooldown_seconds
  read_scaling_instance_size    = var.read_scaling_instance_size
  read_scaling_flock_size       = var.read_scaling_flock_size
  read_scaling_cooldown_seconds = var.read_scaling_cooldown_seconds
}

resource "motherduck_access_token" "edge" {
  username   = motherduck_service_account.edge.username
  name       = var.token_name
  token_type = "read_write"
  ttl        = var.token_ttl

  depends_on = [motherduck_duckling_config.edge]
}

data "motherduck_user_tokens" "edge" {
  username = motherduck_service_account.edge.username

  depends_on = [motherduck_access_token.edge]
}

output "username" {
  value = motherduck_service_account.edge.username
}

output "token_id" {
  value = motherduck_access_token.edge.id
}

output "token_name" {
  value = motherduck_access_token.edge.name
}

output "token_type" {
  value = motherduck_access_token.edge.token_type
}

output "tokens_json" {
  value     = data.motherduck_user_tokens.edge.tokens_json
  sensitive = true
}
