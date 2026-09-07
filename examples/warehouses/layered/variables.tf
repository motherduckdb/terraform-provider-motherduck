variable "environment" {
  description = "Environment label. Use the matching writer service-account token and a separate state."
  type        = string
  default     = "dev"
  nullable    = false
  validation {
    condition     = contains(["dev", "prod"], var.environment)
    error_message = "environment must be dev or prod."
  }
}

variable "name_prefix" {
  description = "Unique lowercase prefix for this example's databases."
  type        = string
  default     = "example_dwh"
  nullable    = false
  validation {
    condition     = can(regex("^[a-z][a-z0-9_]{0,39}$", var.name_prefix))
    error_message = "name_prefix must start with a lowercase letter and use at most 40 lowercase letters, digits, or underscores."
  }
}
