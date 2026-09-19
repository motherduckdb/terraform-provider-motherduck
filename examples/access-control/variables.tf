variable "role_prefix" {
  description = "Prefix for every role this configuration owns. It keeps reviewed roles distinguishable from roles created by hand."
  type        = string
  default     = "analytics"
  nullable    = false
  validation {
    condition     = can(regex("^[a-z][a-z0-9_-]{0,63}$", var.role_prefix))
    error_message = "role_prefix must start with a lowercase letter and contain only lowercase letters, digits, hyphens, and underscores."
  }
}

variable "teams" {
  description = "Access map. Each entry becomes one role, its inherited platform role, its direct members, and the shares it can read."
  type = map(object({
    platform_role = optional(string)
    members       = optional(list(string), [])
    shares        = optional(list(string), [])
  }))
  nullable = false
  default = {
    analysts = {
      platform_role = "explorer"
      members       = ["svc_analytics_reader"]
      shares        = ["analytics_share"]
    }
    engineers = {
      platform_role = "builder"
      members       = ["svc_analytics_writer"]
      shares        = ["analytics_share"]
    }
  }
  validation {
    condition     = length(var.teams) > 0
    error_message = "teams must include at least one team."
  }
  validation {
    condition = alltrue([
      for team in values(var.teams) :
      team.platform_role == null || contains(["admin", "builder", "explorer"], coalesce(team.platform_role, "explorer"))
    ])
    error_message = "platform_role must be admin, builder, or explorer. MotherDuck custom roles inherit platform permissions and cannot select them individually."
  }
  validation {
    condition = alltrue(flatten([
      for team in values(var.teams) : [
        for member in team.members : can(regex("^\\S(.*\\S)?$", member))
      ]
    ]))
    error_message = "Every member must be a non-blank username with no leading or trailing whitespace."
  }
}
