variable "name_prefix" {
  description = "Organization-unique prefix for this disposable deployment."
  type        = string
  default     = "example_cfa"
  nullable    = false
  validation {
    condition     = can(regex("^[a-z][a-z0-9_]{0,39}$", var.name_prefix))
    error_message = "Use a lowercase identifier of at most 40 characters."
  }
}

variable "tenants" {
  description = "Stable internal tenant identifiers. Removing a key destroys its data."
  type        = set(string)
  default     = ["acme", "globex"]
  nullable    = false
  validation {
    condition     = length(var.tenants) > 0 && alltrue([for id in var.tenants : can(regex("^[a-z][a-z0-9_]{0,39}$", id))])
    error_message = "Supply at least one lowercase identifier, each at most 40 characters."
  }
}

variable "expected_writer_username" {
  description = "Expected SQL owner. Set for live use. Null permits an offline example plan without a SQL read."
  type        = string
  default     = null
}
