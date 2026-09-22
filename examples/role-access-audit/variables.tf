variable "expected_roles" {
  description = "Exact direct members (user:name or role:name) and directly inherited roles for each audited role."
  type = map(object({
    members  = set(string)
    inherits = set(string)
  }))
  nullable = false
  validation {
    condition     = length(var.expected_roles) > 0
    error_message = "expected_roles must include at least one role."
  }
  validation {
    condition = alltrue([
      for name, policy in var.expected_roles :
      trimspace(name) == name && length(name) > 0 && (policy == null ? false : (
        policy.members == null || policy.inherits == null ? false : alltrue(concat(
          [for member in policy.members : can(regex("^(user|role):\\S(.*\\S)?$", member))],
          [for role in policy.inherits : role == null ? false : length(trimspace(role)) > 0 && role == trimspace(role)]
        ))
      ))
    ])
    error_message = "Supply non-blank role names, non-null sets, and members as user:name or role:name."
  }
}
variable "fail_on_drift" {
  description = "Reject a plan when the direct access policy differs, even when the same drift is already saved in state."
  type        = bool
  nullable    = false
  default     = false
}
