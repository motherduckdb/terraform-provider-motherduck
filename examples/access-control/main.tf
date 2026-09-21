# One role per team, one resource per grant.
locals {
  # Omit platform-role grants for teams without platform_role.
  inherited_platform_roles = {
    for team_key, team in var.teams : team_key => team.platform_role
    if team.platform_role != null
  }

  memberships = merge([
    for team_key, team in var.teams : {
      for member in team.members :
      "${team_key}/${member}" => { team = team_key, member = member }
    }
  ]...)

  share_reads = merge([
    for team_key, team in var.teams : {
      for share in team.shares :
      "${team_key}/${share}" => { team = team_key, share = share }
    }
  ]...)
}

resource "motherduck_role" "team" {
  for_each = var.teams
  name     = "${var.role_prefix}_${each.key}"
}

# Inherit platform permissions.
resource "motherduck_role_grant" "platform" {
  for_each     = local.inherited_platform_roles
  role_name    = each.value
  grantee_name = motherduck_role.team[each.key].name
  grantee_type = "role"
}

# Grant membership to users or service accounts.
resource "motherduck_role_grant" "member" {
  for_each     = local.memberships
  role_name    = motherduck_role.team[each.value.team].name
  grantee_name = each.value.member
  grantee_type = "user"
}

# Shares must exist and use access = "restricted".
resource "motherduck_share_grant" "team" {
  for_each     = local.share_reads
  share        = each.value.share
  username     = motherduck_role.team[each.value.team].name
  grantee_type = "role"
}
