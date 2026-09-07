# Bootstrap environment identities

Creates two service accounts, dev/prod writers, and two non-expiring tokens on
each: `read_write` for ingestion/Terraform and `read_scaling` for BI. The BI
credentials share their environment writer's readable catalog, including raw
and transform data. They do not require cross-account sharing or attachment.

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

Bootstrap explicitly configures each writer's compute with Standard instances,
60-second idle cooldowns, and maximum read fleets of one replica in dev and two
in prod. Override `read_scaling_flock_size = { dev = 1, prod = 4 }`, for example,
after considering concurrency and cost. The fleet starts on demand.

`tokens` retains the keys `dev_writer`, `dev_bi`, `prod_writer`, and `prod_bi`.
Both dev tokens belong to `usernames.dev_writer`; both prod tokens belong to
`usernames.prod_writer`. Connect with the writer token and apply its warehouse
before the first BI connection to initialize the account. `ttl` is intentionally
omitted so the example needs no expiration/rotation setup; revoke tokens during
cleanup. See the [BI access decision and separate-reader alternative](../README.md#bi-access-decision).

Follow the [shared deployment and cleanup procedure](../README.md). Destroy this
root only after the associated warehouses and shares have been removed.
