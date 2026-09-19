# One role per team, one Terraform resource per direct grant. Every access
# change is then a reviewable line in a pull request rather than a UI click.
locals {
  # Teams that inherit a preset platform role. MotherDuck preset roles are
  # concentric, so admin includes builder and builder includes explorer.
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

# Platform permissions reach a custom role only through inheritance.
resource "motherduck_role_grant" "platform" {
  for_each     = local.inherited_platform_roles
  role_name    = each.value
  grantee_name = motherduck_role.team[each.key].name
  grantee_type = "role"
}

# Membership. Use the service-account username for non-human principals.
resource "motherduck_role_grant" "member" {
  for_each     = local.memberships
  role_name    = motherduck_role.team[each.value.team].name
  grantee_name = each.value.member
  grantee_type = "user"
}

# Data access. The share must already exist and use access = "restricted",
# because organization and unrestricted shares carry their whole audience
# instead of individual grants.
resource "motherduck_share_grant" "team" {
  for_each     = local.share_reads
  share        = each.value.share
  username     = motherduck_role.team[each.value.team].name
  grantee_type = "role"
}
