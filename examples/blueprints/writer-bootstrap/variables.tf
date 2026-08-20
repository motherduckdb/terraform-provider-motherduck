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
