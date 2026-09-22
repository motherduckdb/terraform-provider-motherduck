# Audit role membership

Compare direct members and inheritance against an explicit policy without changing
roles or grants. Read share audiences separately with [the share audit](../share-access-audit/README.md).
A matching share audience alone does not prove that its role has only approved members.

Use an account with permission to inspect these roles. Custom roles require a
Business or Enterprise plan. Supply its SQL token as `MOTHERDUCK_TOKEN`.

Copy this root into its own directory and set `terraform.tfvars`:

```hcl
expected_roles = {
  analytics_analysts = {
    members  = ["user:svc_reader"]
    inherits = ["explorer"]
  }
}
```

Members include direct users and roles that hold this role. `inherits` is the
opposite direction: roles granted to this role. Transitive inheritance appears
as `inherited_roles` for review and is not compared to the direct allowlist.
Do not put inherited roles into the direct policy to silence a difference.

```shell
terraform init
terraform plan -out=audit.tfplan
terraform apply audit.tfplan
terraform output -json audit
terraform plan -var=fail_on_drift=true
```

A failed strict check remains failed even after the same report is saved in
state. A missing or inaccessible role is an error, not an empty policy. Empty
scope and unknown directness are rejected. Audit only after the owning root
finishes applying. Review the expected policy independently of the observed catalog.

This root cannot prove every user's effective access through all other roles or
shares. It never revokes grants. State contains account metadata, so protect it.
Destroying this audit root only removes local report state.
