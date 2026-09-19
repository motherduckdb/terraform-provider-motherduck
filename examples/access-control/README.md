# Access control as code

Turns MotherDuck roles, role membership, and share access into reviewable
Terraform. Each team in `teams` becomes one role, the platform role it inherits,
its direct members, and the shares it can read. Adding a person to a team is a
one-line pull request, and revoking access is the removal of that line.

```hcl
teams = {
  analysts = {
    platform_role = "explorer"
    members       = ["svc_analytics_reader", "dana@example.com"]
    shares        = ["analytics_share"]
  }
}
```

```shell
terraform init
terraform plan -out=access.tfplan
terraform apply access.tfplan
```

The configuration grants access. It does not create the objects being granted.
Shares listed under `shares` must already exist and must use
`access = "restricted"`, because an organization or unrestricted share carries
its whole audience instead of individual grants. Members must already exist as
users or service accounts. The credentials behind `MOTHERDUCK_TOKEN` need
permission to create roles and to grant on the named shares, and share grants
must run as the share owner.

Import grants that already exist before the first apply, otherwise Terraform
plans to create grants that MotherDuck already has:

```shell
terraform import 'motherduck_role_grant.member["analysts/svc_analytics_reader"]' \
  analytics_analysts/user/svc_analytics_reader
terraform import 'motherduck_share_grant.team["analysts/analytics_share"]' \
  analytics_share/role/analytics_analysts
```

`terraform destroy` revokes the grants and drops the roles this configuration
owns. It does not delete users, service accounts, shares, or databases.

The `direct_grants` output lists every grant in a stable form, which is useful
as the artifact of a periodic access review. To read the full audience of a
share, including grants made outside Terraform, use the
[motherduck_share_grants](../../docs/data-sources/share_grants.md) data source.

`github-actions/access-control.yml` runs this root through pull requests. See
[manage access control as code](../../docs/guides/access-control.md) for the
review workflow, credential handling, and what stays outside Terraform.
