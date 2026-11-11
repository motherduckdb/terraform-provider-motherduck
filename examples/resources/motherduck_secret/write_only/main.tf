resource "motherduck_secret" "s3" {
  name = "analytics_s3"
  type = "s3"

  # Sent to MotherDuck on create and whenever params_wo_version changes.
  # Not stored in the plan or the state. Requires Terraform 1.11 or later.
  params_wo = {
    key_id = var.aws_access_key_id
    secret = var.aws_secret_access_key
    region = "us-east-1"
    scope  = "s3://analytics-bucket/"
  }
  params_wo_version = 1
}

variable "aws_access_key_id" {
  type      = string
  sensitive = true
}

variable "aws_secret_access_key" {
  type      = string
  sensitive = true
}
