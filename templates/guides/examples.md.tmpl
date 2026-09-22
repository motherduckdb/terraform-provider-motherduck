---
page_title: "Choose and run an example"
subcategory: "Deployment architectures"
description: |-
  Choose a warehouse, tenant application, access audit, cookbook pipeline, or Pulumi example.
---

# Choose and run an example

Provider v0.2.11 includes the workflows below. These Registry guides explain
configuration and lifecycle behavior. The complete runnable projects, including
Python, Node, SQL templates and dependency locks, live in the version-pinned
GitHub directories linked here. They are examples, not separately published
Terraform Registry modules.

| Goal | Registry guide | Complete v0.2.11 example |
| --- | --- | --- |
| First warehouse | [Create your first warehouse](getting-started.md) | [Simple warehouse](https://github.com/motherduckdb/terraform-provider-motherduck/tree/v0.2.11/examples/warehouses/simple) |
| Dev/prod warehouse | [Warehouse layouts](warehouse-examples.md) and [environments](environments.md) | [Bootstrap and layered roots](https://github.com/motherduckdb/terraform-provider-motherduck/tree/v0.2.11/examples/warehouses) |
| Ingestion and dbt | [Ownership and deployment](deployment-model.md) | [Pinned dlt cookbook and dbt companion](https://github.com/motherduckdb/terraform-provider-motherduck/tree/v0.2.11/examples/cookbook-pipeline) |
| Tenant analytics | [Customer-facing analytics](customer-facing-analytics.md) | [Terraform root and Node backend](https://github.com/motherduckdb/terraform-provider-motherduck/tree/v0.2.11/examples/customer-facing-analytics) |
| Reusable tenant infrastructure | [Authentication and ownership](authentication.md) | [Writer bootstrap](https://github.com/motherduckdb/terraform-provider-motherduck/tree/v0.2.11/examples/blueprints/writer-bootstrap) and [read hypertenancy](https://github.com/motherduckdb/terraform-provider-motherduck/tree/v0.2.11/examples/blueprints/read-hypertenancy) |
| Team roles and grants | [Manage and audit access](access-control.md) | [Access control](https://github.com/motherduckdb/terraform-provider-motherduck/tree/v0.2.11/examples/access-control), [share audit](https://github.com/motherduckdb/terraform-provider-motherduck/tree/v0.2.11/examples/share-access-audit), and [role audit](https://github.com/motherduckdb/terraform-provider-motherduck/tree/v0.2.11/examples/role-access-audit) |
| Pulumi Python | [Signed provider installation](github-installation.md) | [Pinned Python bridge example](https://github.com/motherduckdb/terraform-provider-motherduck/tree/v0.2.11/examples/pulumi/python) |

## Install and select credentials

Terraform 1.5 or later is supported. Declare `motherduckdb/motherduck` as the
provider source and run `terraform init`. Copy every file in a runnable root,
including templates and runtime configuration. For reusable modules, keep the
Git source pinned independently from the provider version:

```hcl
module "writer_bootstrap" {
  source = "github.com/motherduckdb/terraform-provider-motherduck//examples/blueprints/writer-bootstrap?ref=v0.2.11"

  writer_username = "svc_analytics_writer"
}
```

Run identity bootstrap with `MOTHERDUCK_ADMIN_TOKEN`, then provision data with
that writer's `MOTHERDUCK_TOKEN` in a separate root. A service-account resource
does not switch the SQL connection's identity. Keep state and credentials
protected. See [authentication](authentication.md).

## Reader initialization and token rotation

The tenant modules expose sensitive `share_urls`, `reader_setup_tokens` and
`reader_tokens`. Attach each share with its reader's setup token before querying
with its read-scaling token. The one-hour setup credential can be recreated by a
later apply after expiration, so the bootstrap root remains privileged.

Create an overlapping generation, transfer it to consumers and verify their
connections before retiring the previous credential:

```hcl
writer_token_generations = ["rotation_2026_10"]
```

Only in a later apply, after consumer cutover, set:

```hcl
retire_legacy_writer_token = true
```

The writer module includes a moved block for the original token state address.
Reader modules expose equivalent `reader_token_generations` and
`retire_legacy_reader_token` inputs. Terraform does not schedule rotation, and
`-replace` is not an overlapping consumer cutover.

## Suspend without purging

In the customer-facing analytics root, retain tenant IDs and suspend selected
ones explicitly:

```hcl
tenants           = ["acme", "globex"]
suspended_tenants = ["acme"]
```

Disable the tenant's backend route before applying. Suspension removes managed
reader credentials and the share grant while retaining the database, tables,
share, account and compute settings. Clear suspension and complete reader setup
before re-enabling the route. Removing the tenant from `tenants` is a separate,
destructive purge decision. Review retention and export requirements first.

## Keep runtime ownership separate

The cookbook companion creates the database in Terraform. A checksum-pinned
dlt recipe owns ingestion tables, and dbt owns analytical models. Run or deploy
them explicitly after apply. The Flight helper does not run inside Terraform.
Use separate owners, states and database names for dev and prod.

The backend companion uses server-side tenant authentication and separate reader
pools. Its local hashed-session map is a demonstration that must be replaced by
your production identity-provider integration before exposing the service.

## Validate and tear down

Follow the chosen project's README for its build and live result checks. An
empty Terraform plan proves infrastructure consistency, not fresh data or an
absence of out-of-state grants. Use the runtime's data tests and the share/role
audits for those checks.

Stop consumers and schedules first, then delete runtime definitions and review
the data-root destroy plan. Destroy writer bootstrap last. Each example uses
disposable names for validation. Do not turn the teardown procedure into an
unreviewed production deletion workflow.
