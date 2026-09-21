variable "expected_grants" {
  description = "Expected direct audience strings for each share."
  type        = map(set(string))
  nullable    = false

  validation {
    condition     = length(var.expected_grants) > 0
    error_message = "expected_grants must include at least one share."
  }

  validation {
    condition = alltrue([
      for share_name in keys(var.expected_grants) :
      length(trimspace(share_name)) > 0 && trimspace(share_name) == share_name
    ])
    error_message = "Share names must be non-blank and must not have leading or trailing whitespace."
  }

  validation {
    condition = alltrue(flatten([
      for grants in values(var.expected_grants) : grants == null ? [false] : [
        for grant in grants :
        grant == null ? false : (
          contains(["organization:ENTIRE_ORGANIZATION", "domain:ALL_USERS"], grant) ||
          can(regex("^(user|role):[^[:space:]](.*[^[:space:]])?$", grant))
        )
      ]
    ]))
    error_message = "Each audience must be user:<name>, role:<name>, organization:ENTIRE_ORGANIZATION, or domain:ALL_USERS."
  }
}

variable "fail_on_drift" {
  description = "Fail the output precondition when any share differs from its allowlist."
  type        = bool
  nullable    = false
  default     = false
}
