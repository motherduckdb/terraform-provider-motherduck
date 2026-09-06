# Bootstrap environment identities

Creates four service accounts: dev/prod writers and dev/prod BI readers, plus a
30-day token for each. Writer tokens use `read_write`; BI tokens use
`read_scaling`. These token types do not grant access to another account's
database: the layered root adds explicit restricted-share grants.

Requires an authorized `MOTHERDUCK_ADMIN_TOKEN`, a configured filesystem mirror,
and a dedicated protected state. Choose `account_prefix` (default
`svc_example_dwh`) that does not collide with existing accounts.

```shell
terraform init
terraform plan -out=bootstrap.tfplan
terraform apply bootstrap.tfplan
```

Outputs: `usernames` is non-secret; `tokens` is sensitive and creation-only.
Securely transfer each token to the matching environment's secret store before
running another root. Terraform state and saved plan files must remain protected.

This bootstrap uses native service-account compute defaults. Size compute
separately through `motherduck_duckling_config` after measuring the workload;
a new account is not an instruction to reserve large compute.

Follow the [shared deployment and cleanup procedure](../README.md). Destroy this
root only after the associated warehouses and shares have been removed.
