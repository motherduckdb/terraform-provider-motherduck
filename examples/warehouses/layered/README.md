# Layered warehouse with curated BI sharing

One environment writer owns three databases:

- raw: versioned orders from ingestion;
- transform: a view selecting the latest source revision per order;
- marts: a physical daily-revenue table, refreshed by the writer pipeline.

Only marts is published through a hidden, restricted, automatically updated
share. The explicit reader is that environment's BI service account. No raw or
transform share is created. Materializing the marts table keeps consumer queries
independent of cross-database views and source data permissions.

## Apply

Follow the [bootstrap and ownership procedure](../README.md). Set
`reader_username` to the corresponding BI account; it is required and has no
default that could accidentally grant a prod share to a dev reader.

```shell
terraform init
terraform plan -var-file=dev.tfvars -out=warehouse.tfplan
terraform apply warehouse.tfplan
terraform plan -var-file=dev.tfvars -detailed-exitcode
```

There are eight managed resources: three databases, two tables, one view, one
share, and one grant. The final plan should return 0. For prod, use a separate
root/state, prod writer token, and `prod.tfvars`. Adjust the sample reader
usernames if bootstrap used a different prefix.

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

Securely provide `marts_share_url` to the BI client. Using the BI account token,
attach that exact share URL under an alias such as `reporting`, then query
`reporting.main.daily_revenue`. Consumer attachment is a separate SQL/client
operation; the grant alone does not attach a database.

Verify that reads succeed and writes fail. Verify raw/transform and the other
environment cannot be accessed without explicit grants. An automatic share
does not schedule ingestion or marts refresh.

Outputs: `databases`, sensitive `marts_share_url`, `refresh_sql`, `demo_sql`.
The manifest documents the dependency order. Follow the common guide to destroy
warehouse resources before deleting their service accounts.
