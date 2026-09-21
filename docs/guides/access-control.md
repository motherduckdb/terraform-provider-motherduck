---
page_title: "Manage roles and share access"
subcategory: "Operations"
description: |-
  Create MotherDuck roles, assign members, and grant access to shares with Terraform.
---

# Manage roles and share access

Use Terraform to create roles, assign members, and grant access to shares.
The [example](../../examples/access-control/README.md) maps each team to a role
and manages each membership and share grant as a resource.

## Before you start

- Create the users or service accounts that will receive membership.
- Create the shares and set `access = "restricted"` to grant access by user or role.
- Supply `MOTHERDUCK_TOKEN` through your secret manager. The account needs
  permission to create roles and grant membership. Run share grants as the share
  owner. Use a Terraform root per owner when shares have different owners.
- Configure a backend with locking if several people or processes manage the
  same state. Restrict access to state and saved plans.

See [authentication](authentication.md) for credential requirements.

## Define teams and grants

Copy the [example](../../examples/access-control/README.md) into a Terraform
root. Set `teams` in a `.tfvars` file:

```hcl
teams = {
  analysts = {
    platform_role = "explorer"
    members       = ["svc_analytics_reader"]
    shares        = ["analytics_share"]
  }
}
```

This creates the role `analytics_analysts`, grants it `explorer`, adds
`svc_analytics_reader` as a member, and grants the role access to `analytics_share`.
Omit `platform_role` when the role should not inherit platform permissions.

The resources are defined as follows:

```terraform
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
```

## Import roles and grants

Import objects that already exist before applying. For example:

```shell
terraform init
terraform import 'motherduck_role.team["analysts"]' analytics_analysts
terraform import 'motherduck_role_grant.platform["analysts"]' \
  explorer/role/analytics_analysts
terraform import 'motherduck_role_grant.member["analysts/svc_analytics_reader"]' \
  analytics_analysts/user/svc_analytics_reader
terraform import 'motherduck_share_grant.team["analysts/analytics_share"]' \
  analytics_share/role/analytics_analysts
```

Run only the imports for objects that exist. Terraform creates the rest during
apply. See [state and imports](state-and-lifecycle.md) for ownership rules.

## Plan and apply

```shell
terraform init
terraform plan -out=access.tfplan
terraform show access.tfplan
terraform apply access.tfplan
```

Review the plan before applying. Check which roles receive platform permissions,
which users gain membership, and which shares each role can read.

Removing a member or share from `teams` revokes that grant on apply. Other grants
or role memberships can still provide access. Changing a team key or
`role_prefix` replaces its role. Inspect the plan for grants that will be revoked
and recreated.

`terraform destroy` revokes the grants and drops the roles in this state. It does
not delete the users, shares, or databases referenced by this example.

## Inspect share audiences

[motherduck_share_grants](../data-sources/share_grants.md) lists grants to users
and roles, plus audiences created by a share's `access` mode:

```terraform
data "motherduck_share_grants" "analytics" {
  share_name = "analytics_share"
}

output "analytics_readers" {
  value = [
    for grant in data.motherduck_share_grants.analytics.rows :
    "${grant.grantee_type}:${grant.grantee_name}"
  ]
}
```

A role row does not list its members. Use
[motherduck_role_members](../data-sources/role_members.md) for membership and
[motherduck_roles_for_user](../data-sources/roles_for_user.md) for a user's roles,
including inheritance. When reading grants changed in the same configuration,
add `depends_on` for the grant resources so the audit runs after their changes.

Run `terraform plan -detailed-exitcode` to compare configuration and state with
MotherDuck. Exit code `0` means no changes, `1` means an error, and `2` means
Terraform proposes changes. These can include output changes as well as resource
changes.

Terraform reconciles grants it manages. It does not remove grants created
outside its state. Review those through the data sources, then import or revoke
them as appropriate.

## Access boundaries

- Platform permissions and data access are separate. Roles inherit platform
  permissions through `admin`, `builder`, or `explorer`. Shares grant data access.
- `access = "organization"` grants access across the organization.
  `access = "unrestricted"` grants access to anyone with the share URL.
- A share's `include_pattern` selects the tables it exposes. Audiences that need
  different tables need different shares.
- This provider does not manage SSO, provisioning through an identity provider,
  membership of people in an organization, billing, or PrivateLink.

See [sharing and read scaling](sharing-and-read-scaling.md) for share design and
[environments](environments.md) for separating identities and state.
