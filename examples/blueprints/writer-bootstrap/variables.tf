variable "writer_username" {
  description = "Service account username that will own tenant databases and shares."
  type        = string
  nullable    = false

  validation {
    condition     = can(regex("^[A-Za-z][A-Za-z0-9_]{0,254}$", var.writer_username))
    error_message = "writer_username must start with an ASCII letter, contain only ASCII letters, digits, and underscores, and be at most 255 characters."
  }
}

variable "writer_token_name" {
  description = "Name for the generated writer access token."
  type        = string
  default     = "terraform-writer"
  nullable    = false

  validation {
    condition     = length(trimspace(var.writer_token_name)) > 0
    error_message = "writer_token_name must be non-empty."
  }
}

variable "writer_token_ttl_seconds" {
  description = "TTL for the generated writer token."
  type        = number
  default     = 2592000
  nullable    = false

  validation {
    condition     = var.writer_token_ttl_seconds >= 300 && var.writer_token_ttl_seconds <= 31536000
    error_message = "writer_token_ttl_seconds must be between 300 and 31536000 seconds."
  }
}

variable "writer_token_generations" {
  description = "Additional read-write token generations to create during an overlapping rotation."
  type        = set(string)
  default     = []
  nullable    = false

  validation {
    condition = alltrue([
      for generation in var.writer_token_generations :
      can(regex("^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$", generation))
    ])
    error_message = "writer_token_generations must contain non-blank letters, digits, underscores, or hyphens, with at most 64 characters."
  }
}

variable "retire_legacy_writer_token" {
  description = "Revoke the original writer token after a replacement generation is live."
  type        = bool
  default     = false
  nullable    = false
}
