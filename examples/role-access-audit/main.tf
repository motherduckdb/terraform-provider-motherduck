locals {
  role_names = sort(keys(var.expected_roles))
}
data "motherduck_role_members" "audit" {
  count     = length(local.role_names)
  role_name = local.role_names[count.index]
}
data "motherduck_roles_for_role" "audit" {
  count     = length(local.role_names)
  role_name = local.role_names[count.index]
}
locals {
  members = {
    for index, result in data.motherduck_role_members.audit : local.role_names[index] => toset([
      for row in result.rows : "${row.member_type}:${row.member_name}"
    ])
  }
  direct_roles = {
    for index, result in data.motherduck_roles_for_role.audit : local.role_names[index] => toset([
      for row in result.rows : row.role_name if row.is_direct == "true"
    ])
  }
  inherited_roles = {
    for index, result in data.motherduck_roles_for_role.audit : local.role_names[index] => sort([
      for row in result.rows : row.role_name if row.is_direct == "false"
    ])
  }
  valid_directness = alltrue(flatten([
    for result in data.motherduck_roles_for_role.audit : [
      for row in result.rows : row.is_direct == null ? false : contains(["true", "false"], row.is_direct)
    ]
  ]))
  audit = {
    for name, expected in var.expected_roles : name => {
      missing_members    = sort(tolist(setsubtract(expected.members, local.members[name])))
      unexpected_members = sort(tolist(setsubtract(local.members[name], expected.members)))
      missing_roles      = sort(tolist(setsubtract(expected.inherits, local.direct_roles[name])))
      unexpected_roles   = sort(tolist(setsubtract(local.direct_roles[name], expected.inherits)))
      inherited_roles    = local.inherited_roles[name]
    }
  }
  compliant = alltrue([
    for result in local.audit :
    length(result.missing_members) + length(result.unexpected_members) + length(result.missing_roles) + length(result.unexpected_roles) == 0
  ])
}
output "audit" {
  value = local.audit
  precondition {
    condition     = local.valid_directness
    error_message = "Cannot audit role inheritance because the catalog did not report directness."
  }
}
output "compliant" {
  value = local.compliant
  precondition {
    condition     = !var.fail_on_drift || local.compliant
    error_message = "Role access drift detected. Rerun with fail_on_drift=false to inspect the report."
  }
}
