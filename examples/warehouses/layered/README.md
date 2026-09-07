# Layered warehouse with BI read scaling

One environment writer owns three databases:

- raw: versioned orders from ingestion;
- transform: a view selecting the latest source revision per order;
- marts: a physical daily-revenue table, refreshed by the writer pipeline.

BI uses the environment writer's read-scaling token and queries marts directly.
It can also read raw and transform; this layout is not a curated-only access
boundary. The physical marts table keeps dashboard queries independent of the
cross-database transformation view.

## Apply

Follow the [bootstrap and ownership procedure](../README.md). Use the matching
environment's writer token and variables.

```shell
terraform init
terraform plan -var-file=dev.tfvars -out=warehouse.tfplan
terraform apply warehouse.tfplan
terraform plan -var-file=dev.tfvars -detailed-exitcode
```

There are six managed resources: three databases, two tables, and one view.
The final plan should return 0. For prod, use a separate root/state, prod writer
token, and `prod.tfvars`.

## Ingest and refresh

In an empty disposable dev warehouse, run the SQL from
`terraform output -raw demo_sql` using its writer token. Then run
`terraform output -raw refresh_sql` through the same writer's SQL client.

Expect one row in marts.main.daily_revenue: 2026-01-01, two completed orders,
revenue 145.00. The latest o1 revision replaces 100.00 with 120.00; cancelled o3
is excluded. Rerunning refresh keeps the same result. Do not rerun seed SQL.

The refresh uses one transaction to replace table data with DELETE/INSERT.
It does not recreate a Terraform-owned table. Run one publisher per environment,
roll back and fail the pipeline on errors, and schedule refresh outside Terraform.
For large data volumes, adopt incremental modeling with a deliberate ownership
handoff rather than extending this full-refresh demo indefinitely.

The source contract requires unique (order_id, source_revision) and one currency.
The provider does not enforce keys or NOT NULL through its column-type map.
Reject duplicate revisions and invalid required values in the ingestion pipeline.

## Consumer verification

Configure BI with the matching writer's `dev_bi` or `prod_bi` read-scaling token.
Query `<marts_database>.main.daily_revenue` directly, using the `databases` output
to select the name. No share grant or attachment is needed. Read replicas are
eventually consistent, so allow synchronization time after refreshing marts.

Verify reads succeed and writes fail. Raw/transform reads in the same environment
are expected; the other environment should remain inaccessible unless explicitly
shared. For curated-only access, follow the
[separate-reader alternative](../README.md#alternative-separate-readers-with-curated-shares),
including its initial attachment using the reader's read-write token.

Outputs: `databases`, `refresh_sql`, `demo_sql`.
The manifest documents the dependency order. Follow the common guide to destroy
warehouse resources before deleting their service accounts.
