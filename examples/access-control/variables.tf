variable "role_prefix" {
  description = "Prefix for role names created by this configuration."
  type        = string
  default     = "analytics"
  nullable    = false
  validation {
    condition     = can(regex("^[a-z][a-z0-9_-]{0,63}$", var.role_prefix))
    error_message = "role_prefix must start with a lowercase letter and contain only lowercase letters, digits, hyphens, and underscores."
  }
}

variable "teams" {
  description = "Maps each team to a role, its platform role, members, and shares."
  type = map(object({
    platform_role = optional(string)
    members       = optional(set(string), [])
    shares        = optional(set(string), [])
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
      for team_key in keys(var.teams) :
      can(regex("^[a-z][a-z0-9_-]*$", team_key)) &&
      length(team_key) <= 190
    ])
    error_message = "Team keys must start with a lowercase letter, contain only lowercase letters, digits, hyphens, and underscores, and contain at most 190 characters."
  }
  validation {
    condition = alltrue([
      for team in values(var.teams) :
      team.platform_role == null ? true : contains(["admin", "builder", "explorer"], team.platform_role)
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
