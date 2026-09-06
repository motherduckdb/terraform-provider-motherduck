variable "account_prefix" {
  description = "Unique service-account prefix; resulting accounts represent environment ownership."
  type        = string
  default     = "svc_example_dwh"
  nullable    = false
  validation {
    condition     = can(regex("^[a-z][a-z0-9_]{0,39}$", var.account_prefix))
    error_message = "account_prefix must be a lowercase identifier of at most 40 characters."
  }
}
