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

variable "path" {
  type = string
}

variable "include_bucket_listing" {
  type    = bool
  default = false
}

variable "bucket_secret_name" {
  type    = string
  default = "__default_s3"
}

data "motherduck_files" "public_dataset" {
  path = var.path
}

data "motherduck_buckets_for_secret" "default_s3" {
  count       = var.include_bucket_listing ? 1 : 0
  secret_name = var.bucket_secret_name
}

locals {
  file_rows   = nonsensitive(jsondecode(data.motherduck_files.public_dataset.rows_json))
  bucket_rows = var.include_bucket_listing ? nonsensitive(jsondecode(data.motherduck_buckets_for_secret.default_s3[0].rows_json)) : []
}

output "file_row_count" {
  value = length(local.file_rows)
}

output "bucket_row_count" {
  value = length(local.bucket_rows)
}
