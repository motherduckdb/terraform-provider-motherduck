variable "database_prefix" {
  description = "Prefix for tenant database names."
  type        = string
  default     = "tenant"
  nullable    = false

  validation {
    condition     = can(regex("^[A-Za-z][A-Za-z0-9_]{0,119}$", var.database_prefix))
    error_message = "database_prefix must start with an ASCII letter, contain only ASCII letters, digits, and underscores, and be at most 120 characters."
  }
}

variable "reader_prefix" {
  description = "Prefix for tenant reader service account usernames."
  type        = string
  default     = "svc_reader"
  nullable    = false

  validation {
    condition     = can(regex("^[A-Za-z][A-Za-z0-9_]{0,119}$", var.reader_prefix))
    error_message = "reader_prefix must start with an ASCII letter, contain only ASCII letters, digits, and underscores, and be at most 120 characters."
  }
}

variable "share_prefix" {
  description = "Prefix for tenant share names."
  type        = string
  default     = "share"
  nullable    = false

  validation {
    condition     = can(regex("^[A-Za-z][A-Za-z0-9_]{0,119}$", var.share_prefix))
    error_message = "share_prefix must start with an ASCII letter, contain only ASCII letters, digits, and underscores, and be at most 120 characters."
  }
}

variable "reader_token_ttl_seconds" {
  description = "TTL for generated tenant reader tokens."
  type        = number
  default     = 2592000
  nullable    = false

  validation {
    condition     = var.reader_token_ttl_seconds >= 300 && var.reader_token_ttl_seconds <= 31536000
    error_message = "reader_token_ttl_seconds must be between 300 and 31536000 seconds."
  }
}

variable "reader_token_generations" {
  description = "Additional read-scaling token generations to create during an overlapping rotation."
  type        = set(string)
  default     = []
  nullable    = false

  validation {
    condition = alltrue([
      for generation in var.reader_token_generations :
      can(regex("^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$", generation))
    ])
    error_message = "reader_token_generations must contain non-blank letters, digits, underscores, or hyphens, with at most 64 characters."
  }
}

variable "retire_legacy_reader_token" {
  description = "Revoke the original terraform-reader token after a replacement generation is live."
  type        = bool
  default     = false
  nullable    = false
}

variable "tenants" {
  description = "Tenant definitions keyed by stable tenant id."
  type = map(object({
    display_name            = optional(string)
    slug                    = optional(string)
    snapshot_retention_days = optional(number, 7)
  }))
  nullable = false

  validation {
    condition     = length(var.tenants) > 0
    error_message = "tenants must include at least one tenant."
  }

  validation {
    condition = alltrue([
      for tenant_id, tenant in var.tenants :
      length(trimspace(tenant_id)) > 0 &&
      length(replace(lower(coalesce(tenant.slug, tenant_id)), "/[^a-z0-9_]/", "_")) > 0 &&
      length(replace(lower(coalesce(tenant.slug, tenant_id)), "/[^a-z0-9_]/", "_")) <= 120 &&
      tenant.snapshot_retention_days >= 0
    ])
    error_message = "Each tenant key must be non-empty. The generated slug must be 1-120 characters after normalization. The snapshot_retention_days value must be nonnegative."
  }

  validation {
    condition = length(distinct([
      for tenant_id, tenant in var.tenants :
      replace(lower(coalesce(tenant.slug, tenant_id)), "/[^a-z0-9_]/", "_")
    ])) == length(var.tenants)
    error_message = "Tenant keys or slugs must remain unique after normalization. Two tenants such as \"acme-1\" and \"acme_1\" would otherwise generate the same database, share, and service-account names."
  }
}
