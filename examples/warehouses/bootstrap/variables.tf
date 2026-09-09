variable "account_prefix" {
  description = "Unique service-account prefix. Resulting accounts represent environment ownership."
  type        = string
  default     = "svc_example_dwh"
  nullable    = false
  validation {
    condition     = can(regex("^[a-z][a-z0-9_]{0,39}$", var.account_prefix))
    error_message = "account_prefix must be a lowercase identifier of at most 40 characters."
  }
}

variable "read_scaling_flock_size" {
  description = "Maximum BI read replicas per environment. Tune for concurrency and cost."
  type        = object({ dev = number, prod = number })
  default     = { dev = 1, prod = 2 }
  nullable    = false
  validation {
    condition = alltrue([
      for size in values(var.read_scaling_flock_size) :
      size != null && try(size >= 1 && size <= 16 && floor(size) == size, false)
    ])
    error_message = "Each environment needs an integer read-scaling fleet size between 1 and 16."
  }
}
