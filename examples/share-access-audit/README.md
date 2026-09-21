# Audit share audiences

Compare the direct audiences of existing shares with an explicit allowlist. This
read-only Terraform root reports missing and unexpected grants, including
organization-wide or public access. It does not create or revoke grants.

This example requires provider v0.2.10 or later, which includes
`motherduck_share_grants`. Terraform 1.5 or later is supported.

Copy this directory into a separate Terraform root and define every intended
audience for each share in `terraform.tfvars`:

```hcl
expected_grants = {
  analytics_share = ["role:analytics_analysts", "user:svc_reader"]
  private_share   = []
}
```

An empty list means the share should have no grantees. An empty map is rejected
so an audit cannot silently check nothing. User and role names are distinct,
even when their text is identical. For deliberately broader shares, allow
`organization:ENTIRE_ORGANIZATION` or `domain:ALL_USERS` explicitly.

Run with the share owner's credentials, or an organization admin able to inspect
the shares. Supply `MOTHERDUCK_TOKEN` through your secret manager. Restrict access
to local state and saved plans because they contain account metadata.

```shell
terraform init
terraform plan -out=audit.tfplan
terraform show audit.tfplan
terraform apply audit.tfplan
terraform output -json audit
```

The `audit` output lists `missing` and `unexpected` audiences for each named
share. `compliant` is true only when all allowlists match. Applying this root
only saves the observed report to state. It does not repair access.

To fail the plan whenever a mismatch exists:

```shell
terraform plan -var=fail_on_drift=true
```

This check fails even if a previous apply saved the same drift. Without it,
`plan -detailed-exitcode` can return zero for an unchanged report that still
contains drift. Rerun with `-var=fail_on_drift=false` to inspect the full report
after a failed check. Do not use `-refresh=false` when auditing current access.

Audit after applying the configuration that manages grants. This separate root
has no dependency on that configuration. An inaccessible or missing share is
an error, not an empty audience list. Only the shares in `expected_grants` are
checked. Review the allowlist independently rather than copying live audiences
into it and treating a match as approval.

These are direct share audiences. Role membership, inheritance, share ownership,
and access through other shares require separate review. Follow
[the access-control guide](../../docs/guides/access-control.md) to review those
boundaries and decide which grants to import or revoke.
