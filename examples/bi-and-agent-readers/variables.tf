variable "name_prefix" {
  description = "Organization-unique prefix for this disposable deployment."
  type        = string
  default     = "example_readers"
  nullable    = false
  validation {
    condition     = can(regex("^[a-z][a-z0-9_]{0,39}$", var.name_prefix))
    error_message = "Use a lowercase identifier of at most 40 characters."
  }
}

variable "consumers" {
  description = "One entry per BI tool, application, or AI agent that reads the marts. Removing a key deletes its service account and tokens."
  type = map(object({
    purpose           = string
    instance_size     = optional(string, "standard")
    flock_size        = optional(number, 1)
    token_ttl_seconds = optional(number, 2592000)
  }))
  default = {
    dashboards = {
      purpose    = "BI dashboards"
      flock_size = 2
    }
    notebooks = {
      purpose = "Analyst notebooks"
    }
    support_agent = {
      purpose           = "AI agent over an MCP server"
      token_ttl_seconds = 604800
    }
  }
  nullable = false

  validation {
    condition     = length(var.consumers) > 0 && alltrue([for key in keys(var.consumers) : can(regex("^[a-z][a-z0-9_]{0,39}$", key))])
    error_message = "Supply at least one consumer. Keys are lowercase identifiers of at most 40 characters."
  }
  validation {
    condition     = alltrue([for consumer in values(var.consumers) : contains(["standard", "jumbo"], consumer.instance_size)])
    error_message = "instance_size must be standard or jumbo, the read-scaling sizes this example supports."
  }
  validation {
    condition     = alltrue([for consumer in values(var.consumers) : consumer.flock_size >= 1 && consumer.flock_size <= 64])
    error_message = "flock_size must be between 1 and 64."
  }
  validation {
    condition     = alltrue([for consumer in values(var.consumers) : consumer.token_ttl_seconds >= 300 && consumer.token_ttl_seconds <= 31536000])
    error_message = "token_ttl_seconds must be between 300 and 31536000 seconds."
  }
}

variable "expected_writer_username" {
  description = "Expected SQL owner. Set for live use. Null permits an offline example plan without a SQL read."
  type        = string
  default     = null
}
