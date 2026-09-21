# Roles and share access

Each entry in `teams` creates a role, grants its platform role, adds members,
and grants access to shares. Users and shares must already exist. Shares must
use `access = "restricted"`.

Set `teams` in a `.tfvars` file:

```hcl
teams = {
  analysts = {
    platform_role = "explorer"
    members       = ["svc_analytics_reader"]
    shares        = ["analytics_share"]
  }
}
```

Supply `MOTHERDUCK_TOKEN` through your secret manager. The account needs
permission to create roles and grant membership. Share grants must run as the
share owner. Use a backend with locking when several people or processes manage
the same state.

Import roles and grants that already exist before applying. For example:

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

Run only the imports for objects that exist, then review and apply the plan:

```shell
terraform plan -out=access.tfplan
terraform show access.tfplan
terraform apply access.tfplan
```

The `role_names` output maps teams to role names. `direct_grants` lists grants
managed by this configuration. To inspect grants outside its state, use
[motherduck_share_grants](../../docs/data-sources/share_grants.md).

Removing a membership revokes that grant. Other grants or role memberships can
still provide access. `terraform destroy` revokes the grants and drops the roles
in this state. It does not delete users, shares, or databases.

See [manage roles and share access](../../docs/guides/access-control.md) for
access boundaries and audit queries.
