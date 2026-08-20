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
  suffix      = replace(var.run_id, "-", "_")
  secret_name = "tf_secret_drift_s3_${local.suffix}"
}

resource "motherduck_secret" "managed_s3" {
  name = local.secret_name
  type = "s3"

  params = {
    key_id = "terraform-drift-key"
    region = "us-east-1"
    scope  = "s3://terraform-provider-motherduck/secret-drift/${var.run_id}/managed/"
    secret = "terraform-drift-secret"
  }
}

output "secret_name" {
  value = motherduck_secret.managed_s3.name
}

output "secret_scope" {
  value = motherduck_secret.managed_s3.scope
}
