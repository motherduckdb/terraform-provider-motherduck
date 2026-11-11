variable "name_prefix" {
  description = "Organization-unique prefix for this disposable deployment."
  type        = string
  default     = "example_lake"
  nullable    = false
  validation {
    condition     = can(regex("^[a-z][a-z0-9_]{0,39}$", var.name_prefix))
    error_message = "Use a lowercase identifier of at most 40 characters."
  }
}

variable "data_path" {
  description = "S3 prefix that holds the DuckLake data files, ending in a slash. Use a prefix that nothing else writes to."
  type        = string
  nullable    = false
  validation {
    condition     = can(regex("^s3://[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]/(.+/)?$", var.data_path))
    error_message = "Use an s3:// URL that ends in a slash, for example s3://my-bucket/lake/."
  }
}

variable "bucket_region" {
  description = "AWS region of the bucket. It can differ from the MotherDuck organization's region."
  type        = string
  nullable    = false
}

variable "aws_access_key_id" {
  description = "Access key ID of an IAM identity limited to data_path."
  type        = string
  sensitive   = true
  nullable    = false
}

variable "aws_secret_access_key" {
  description = "Secret access key of that IAM identity."
  type        = string
  sensitive   = true
  nullable    = false
}

variable "credentials_version" {
  description = "Change this number whenever the access key changes. Terraform does not store the key, so this is how it knows to write a new one."
  type        = number
  default     = 1
  nullable    = false
}
