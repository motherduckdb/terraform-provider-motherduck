resource "motherduck_secret" "s3" {
  name = "analytics_s3"
  type = "s3"

  params = merge(
    {
      key_id = var.aws_access_key_id
      secret = var.aws_secret_access_key
      region = "us-east-1"
      scope  = "s3://analytics-bucket/"
    },
    var.aws_session_token == null ? {} : { session_token = var.aws_session_token }
  )
}

variable "aws_access_key_id" {
  type      = string
  sensitive = true
}

variable "aws_secret_access_key" {
  type      = string
  sensitive = true
}

variable "aws_session_token" {
  type      = string
  sensitive = true
  default   = null
}
