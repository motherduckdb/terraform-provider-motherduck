---
page_title: "Build a layered data warehouse"
subcategory: "Deployment architectures"
description: |-
  Deploy raw, transformation, and marts databases and refresh a daily-revenue model.
---

# Build a layered data warehouse

Use a layered warehouse when source records arrive more than once, business
logic needs a clear home, and dashboards should query prepared tables. This
guide uses an orders feed with source revisions and a physical revenue mart.

![A writer loads raw orders, deduplicates them through a transform view, and refreshes a physical revenue mart. BI uses the writer's read-scaling token.](https://raw.githubusercontent.com/motherduckdb/terraform-provider-motherduck/main/docs/assets/layered-warehouse.png)

## Choose a starting point

| Example | Layout | Use it for |
| --- | --- | --- |
| [Simple](https://github.com/motherduckdb/terraform-provider-motherduck/tree/main/examples/warehouses/simple) | One database, raw table, analytics view | A small feed with one row per order |
| [Layered](https://github.com/motherduckdb/terraform-provider-motherduck/tree/main/examples/warehouses/layered) | Three databases, two tables, one view | Revised source records and a separately refreshed mart |
| [Bootstrap](https://github.com/motherduckdb/terraform-provider-motherduck/tree/main/examples/warehouses/bootstrap) | Dev/prod writers, tokens, compute settings | Dedicated ownership for either layout |

The steps below use the layered root. Follow [environment setup](environments.md)
first and inject the selected writer's token as `MOTHERDUCK_TOKEN`. The warehouse
root does not need an admin credential.

## 1. Provision the layers

Copy `examples/warehouses/layered`, including its SQL templates and variable
files, into your infrastructure repository. Configure your backend before using
production data. The checked-in example otherwise uses local state.

```shell
terraform init
terraform plan -var-file=dev.tfvars -out=warehouse.tfplan
terraform apply warehouse.tfplan
terraform plan -var-file=dev.tfvars -detailed-exitcode
```

Expect six creations, followed by a no-change plan. The default database names
are `example_dwh_dev_raw`, `example_dwh_dev_transform`, and
`example_dwh_dev_marts`. All three belong to the authenticated writer.

| Relation | Responsibility | Definition owner |
| --- | --- | --- |
| `raw.main.orders` | Versioned source rows with `source_revision` | Terraform |
| `transform.main.orders_latest` | Latest revision for each order | Terraform view generated from SQL |
| `marts.main.daily_revenue` | Materialized daily counts and totals | Terraform table, pipeline-owned rows |

The shortened database names in this table describe the layers. Use the full
physical names from `terraform output databases` in SQL.

## 2. Load and transform orders

For an empty disposable dev warehouse, run the SQL from
`terraform output -raw demo_sql` as the writer. Then run the SQL from
`terraform output -raw refresh_sql` with the same identity.

The transformation keeps the latest revision for each order:

```sql
SELECT order_id, order_date, amount, status
FROM "${raw_database}".main.orders
WHERE order_id IS NOT NULL AND source_revision IS NOT NULL
QUALIFY row_number() OVER (PARTITION BY order_id ORDER BY source_revision DESC) = 1
```

Terraform's `templatefile` expands `${raw_database}`. Do not paste the unrendered
template into a SQL client. The example's `model_manifest.yml` records the input
and output dependencies.

Query the resulting mart:

```sql
SELECT order_date, order_count, revenue
FROM example_dwh_dev_marts.main.daily_revenue
ORDER BY order_date;
```

| order_date | order_count | revenue |
| --- | --- | --- |
| 2026-01-01 | 2 | 145.00 |

The revised order contributes 120.00 instead of 100.00, another contributes
25.00, and the cancelled order is excluded. Rerunning the refresh should keep
this result. Do not rerun the seed.

## 3. Schedule refresh outside Terraform

The refresh replaces **rows** inside a transaction using `DELETE` and `INSERT`.
It leaves the Terraform-owned table definition intact. Run it from your scheduler
or a Flight as the writer, with one publisher per environment.

The source contract requires unique `(order_id, source_revision)` pairs,
required values, and a single currency. Enforce these conditions in ingestion.
The provider's column-type map does not create primary keys or `NOT NULL`
constraints.

For a larger warehouse, use an incremental modeling tool such as dbt when
appropriate. Transfer ownership of its tables and views deliberately. Keep
Terraform for the databases and access boundaries, and let the modeling tool
own its definitions. See [resource ownership](resource-scope.md).

## 4. Connect BI

The bootstrap creates a `dev_bi` read-scaling token on the **dev writer account**.
Configure BI with that token and query marts directly. This moves reads onto
the writer's read-scaling pool, but it can also read raw and transform data
visible to that account.

Use a separate reader account and a restricted marts share when BI must only
see curated data. Follow [sharing and read scaling](sharing-and-read-scaling.md),
including the first attachment with the reader's read-write token.

Physical marts are useful at this boundary: consumers do not need access to the
raw database referenced by the transformation view. Shares and read replicas
can lag behind writes, so verify freshness with the actual BI credential.

## 5. Promote and operate

Apply the same reviewed configuration in a separate production state with the
production writer token and `prod.tfvars`. Promote code and validate data under
the destination account. Do not copy Terraform state between environments.

Review table replacement plans before changing columns. Protect important
databases and tables with an appropriate lifecycle policy and a recovery plan.
For disposable cleanup, destroy the warehouse with its original writer token
before deleting bootstrap accounts.

The repository checks these SQL models against embedded DuckDB for revised
orders, cancellation, exact decimal totals, empty input, and repeatable refresh.
Those checks do not prove your cross-account permissions or production sizing.
Use each intended reader credential to verify both allowed and denied reads.
