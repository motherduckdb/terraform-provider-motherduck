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

variable "username" {
  type = string
}

variable "token_id" {
  type = string
}

variable "token_name" {
  type = string
}

variable "token_type" {
  type = string
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

resource "motherduck_service_account" "imported" {
  username = var.username
}

resource "motherduck_duckling_config" "imported" {
  username = motherduck_service_account.imported.username

  read_write_instance_size      = var.read_write_instance_size
  read_write_cooldown_seconds   = var.read_write_cooldown_seconds
  read_scaling_instance_size    = var.read_scaling_instance_size
  read_scaling_flock_size       = var.read_scaling_flock_size
  read_scaling_cooldown_seconds = var.read_scaling_cooldown_seconds
}

resource "motherduck_access_token" "imported" {
  username   = motherduck_service_account.imported.username
  name       = var.token_name
  token_type = var.token_type
}

output "imported_username" {
  value = motherduck_service_account.imported.username
}

output "imported_token_id" {
  value = var.token_id
}
