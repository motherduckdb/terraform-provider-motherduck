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

variable "secret_scope" {
  type = string
}

locals {
  suffix      = replace(var.run_id, "-", "_")
  secret_name = "tf_raw_s3_${local.suffix}"
}

resource "motherduck_secret" "raw_s3" {
  name = local.secret_name
  type = "s3"

  secret_sql = <<-SQL
    KEY_ID 'terraform-raw-key',
    SECRET 'terraform-raw-secret',
    REGION 'us-east-1',
    SCOPE '${var.secret_scope}'
  SQL
}

data "motherduck_secrets" "raw_s3" {
  name = motherduck_secret.raw_s3.name

  depends_on = [motherduck_secret.raw_s3]
}

output "secret_name" {
  value = motherduck_secret.raw_s3.name
}

output "secret_scope" {
  value = motherduck_secret.raw_s3.scope
}

output "secret_rows_json" {
  value     = data.motherduck_secrets.raw_s3.rows_json
  sensitive = true
}
