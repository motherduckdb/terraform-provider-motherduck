---
page_title: "Choose a deployment architecture"
subcategory: "Deployment architectures"
description: |-
  Understand account ownership, compute, storage, and deployment responsibilities on MotherDuck.
---

# Choose a deployment architecture

A MotherDuck deployment starts with an account that owns data and runs queries.
Terraform creates its infrastructure. A pipeline writes data, and readers query
that data using credentials chosen for their workload.

Unlike a database name, an account selects both ownership and compute. Two
databases created with the same writer token share that account's read-write
Duckling. Creating another token for the account does not create another writer.

![An admin provisions accounts and compute. The writer owns warehouse databases, and readers consume published data through their own compute.](https://raw.githubusercontent.com/motherduckdb/terraform-provider-motherduck/main/docs/assets/deployment-model.png)

## The building blocks

| Building block | What it means for a deployment | Terraform surface |
| --- | --- | --- |
| Organization | Existing regional and billing boundary | Organization creation is outside this provider |
| Account | Owns databases, secrets, and compute | `motherduck_service_account` |
| Duckling | An account's read-write compute or read-scaling pool | `motherduck_duckling_config` |
| Token | Authenticates as an account and selects a supported access mode | `motherduck_access_token` |
| Database | Contains schemas, tables, and views owned by its creator | `motherduck_database` |
| Share | Publishes read-only data for other accounts | `motherduck_share`, `motherduck_share_grant` |

The SQL identity comes from `MOTHERDUCK_TOKEN`, not from an adjacent service
account resource. Provisioning `svc_writer_prod` with an admin token does not
switch Terraform's SQL connection to that account.
See [MotherDuck resource management](https://motherduck.com/docs/concepts/resource-management/).

## Choose the smallest useful topology

| Workload | Starting architecture | When to grow it |
| --- | --- | --- |
| A team dashboard over orders | One writer, one database, raw and analytics schemas | Add separate databases when publication or model ownership needs a boundary |
| A warehouse with several models | Raw, transform, and physical marts databases | Separate ingestion and transformation accounts when they need independent compute or ownership |
| Many dashboard connections to the same data | Read-scaling pool on the account that can see that data | Use a separate reader account and curated share when readers must not see raw data |
| Analytics for several customers | Central writer, one database and restricted share per customer, separate readers | Move a customer's writes to a separate owner if writer compute also needs isolation |
| Python pipelines and dashboards released together | Terraform platform plus Blueprints application packages | Add separate preview/staging credentials and release-based production promotion |

The [warehouse examples](warehouse-examples.md) start with native MotherDuck
storage. Choose DuckLake deliberately when an open storage format or object
storage arrangement requires it. It is not a prerequisite for layering data or
serving customer analytics.

## Separate infrastructure, definitions, and execution

| Responsibility | Recommended owner | Examples |
| --- | --- | --- |
| Platform infrastructure | Terraform | Service accounts, tokens, compute settings, databases, access grants |
| Warehouse definitions | Terraform for simple static schemas, or a modeling tool | Tables and views, owned by exactly one tool |
| Application content | Blueprints or an application deployment pipeline | Flight Python, Dive components, Guide Markdown |
| Data operations | Ingestion and transformation runtime | Loading orders, refreshing marts, running backfills |

This division keeps an infrastructure apply from unexpectedly rerunning a load.
It also lets a dashboard change ship without giving its author organization
administration credentials.

Terraform can own Flight definitions and schedules when that is your chosen
workflow. Creating the definition is separate from running it. See
[resource scope](resource-scope.md) for the supported ownership choices.

## Deploy in stages

1. **Bootstrap identities.** An administrator creates service accounts, tokens,
   and compute settings in a restricted Terraform state.
2. **Provision as the owner.** Inject the writer token into a separate root and
   create the databases, schemas, and publication boundaries.
3. **Deploy and run the workload.** Pass database names to the pipeline, then
   load data and verify its result.
4. **Connect readers.** Grant and attach shares where needed, then configure BI
   or the application backend with reader credentials.

Teardown reverses these dependencies. Remove consumers and workload data before
revoking the writer token or deleting its account.

## Common misconceptions

| Assumption | What to do instead |
| --- | --- |
| A Terraform workspace named `prod` switches MotherDuck identity | Select the production token and backend explicitly |
| A database per customer also isolates all writes | Use separate writer accounts if writer compute must be isolated |
| A second token restricts which tables an account can read | Give a separate reader account access to an appropriate share |
| A share grant makes the data immediately queryable | Attach the share as the reader, then allow for publication and replica synchronization |
| A successful apply means the warehouse has fresh data | Run and verify ingestion or transformation separately |

Continue with [your first warehouse](getting-started.md),
[environment separation](environments.md), or
[customer-facing analytics](customer-facing-analytics.md).
