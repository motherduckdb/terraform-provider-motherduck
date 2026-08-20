terraform {
  required_version = ">= 1.5.0"

  required_providers {
    motherduck = {
      source  = "motherduckdb/motherduck"
      version = "= 0.1.0"
    }
  }
}

variable "database_type" {
  type = string
}

variable "cooldown_seconds" {
  type = number
}

resource "motherduck_database" "deferred" {
  name          = "deferred_validation"
  database_type = var.database_type
  data_path     = "s3://example-bucket/ducklake"
  encrypted     = true
}

resource "motherduck_duckling_config" "deferred" {
  username                      = "deferred_validation"
  read_write_instance_size      = "pulse"
  read_write_cooldown_seconds   = var.cooldown_seconds
  read_scaling_instance_size    = "pulse"
  read_scaling_flock_size       = 1
  read_scaling_cooldown_seconds = var.cooldown_seconds
}
