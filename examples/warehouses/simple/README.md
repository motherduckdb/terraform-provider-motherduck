# Simple orders warehouse

A small warehouse owned by one environment writer:
`raw.orders` → `analytics.daily_revenue`, inside one database.

This is a schema/layout example, not a BI security boundary. Raw and analytics
schemas share database ownership. Use the layered example when only curated
data should be shared with another account.

## Apply

Follow [bootstrap and identity selection](../README.md) first. Inject the dev
writer token. In this root, choose `name_prefix` (default `example_dwh`) and
`environment` (default `dev`).

```shell
terraform init
terraform plan -var-file=dev.tfvars -out=warehouse.tfplan
terraform apply warehouse.tfplan
terraform plan -var-file=dev.tfvars -detailed-exitcode
```

The final command should return 0, with no changes. There are five managed
resources: one database, two schemas, one table, and one view.

## Load a disposable demo

After apply, obtain `terraform output -raw demo_sql`. Run that SQL with the
same writer identity only in an empty disposable warehouse. It inserts three
orders; the revenue view returns one row for 2026-01-01, with two completed
orders and revenue 125.00. Running seed SQL twice duplicates input rows.
The view assumes one row per order_id and a single currency.

`model_manifest.yml` describes dependencies; Terraform manages the view from
`daily_revenue.sql.tftpl`. Types use DATE and DECIMAL instead of string dates or
floating-point money. The provider's column map expresses types, not key or
NOT NULL constraints: enforce source uniqueness and required values at ingestion.

Outputs: `database_name`, `revenue_relation`, and `demo_sql`.
For prod, use a separate root/state and prod writer token with `prod.tfvars`.
See the [lifecycle cautions](../README.md) before schema changes or destruction.
