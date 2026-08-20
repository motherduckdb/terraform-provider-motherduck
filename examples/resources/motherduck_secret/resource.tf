resource "motherduck_secret" "s3" {
  name = "analytics_s3"
  type = "s3"

  params = {
    key_id = var.aws_access_key_id
    secret = var.aws_secret_access_key
    region = "us-east-1"
    scope  = "s3://analytics-bucket/"
  }
}

variable "aws_access_key_id" {
  type      = string
  sensitive = true
}

variable "aws_secret_access_key" {
  type      = string
  sensitive = true
}

