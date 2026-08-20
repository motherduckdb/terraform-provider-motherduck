variable "expected_writer_username" {
  description = "Optional guard: fail the plan when the provider's SQL identity does not match this writer service account username. Leave null to skip the check, for example when the SQL session reports a generic identity such as \"duckdb\"."
  type        = string
  default     = null

  validation {
    condition     = var.expected_writer_username == null || can(regex("^[A-Za-z][A-Za-z0-9_]{0,254}$", var.expected_writer_username))
    error_message = "expected_writer_username must start with an ASCII letter, contain only ASCII letters, digits, and underscores, and be at most 255 characters."
  }
}

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
    error_message = "Each tenant key must be non-empty; the generated slug must be 1-120 characters after normalization; snapshot_retention_days must be nonnegative."
  }

  validation {
    condition = length(distinct([
      for tenant_id, tenant in var.tenants :
      replace(lower(coalesce(tenant.slug, tenant_id)), "/[^a-z0-9_]/", "_")
    ])) == length(var.tenants)
    error_message = "Tenant keys or slugs must remain unique after normalization; two tenants such as \"acme-1\" and \"acme_1\" would otherwise generate the same database, share, and service-account names."
  }
}
