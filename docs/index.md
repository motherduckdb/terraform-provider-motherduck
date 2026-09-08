---
page_title: "MotherDuck Provider"
description: |-
  Build data warehouses and customer-facing analytics on MotherDuck with Terraform.
---

# MotherDuck Provider

Build a warehouse for your team or an analytics backend for your customers.
The MotherDuck provider manages the databases, identities, compute settings,
and access controls that those workloads run on.

[MotherDuck](https://motherduck.com/docs/) runs DuckDB in the cloud. Each user
or service account has its own compute and owns the databases it creates.
Shares let other accounts query published data on their own compute.

## Choose what to build

| Your next step | What you will build |
| --- | --- |
| [Create your first warehouse](guides/getting-started.md) | An orders table and a daily-revenue view, with sample data and a queryable result |
| [Choose a deployment architecture](guides/deployment-model.md) | Decide which accounts own data, which tools deploy it, and where queries run |
| [Build a layered warehouse](guides/warehouse-examples.md) | Raw, transformation, and marts databases with a repeatable refresh |
| [Separate development and production](guides/environments.md) | Independent writer identities, credentials, compute settings, and Terraform state |
| [Serve customer-facing analytics](guides/customer-facing-analytics.md) | One database and restricted share per customer, queried through tenant-specific readers |
| [Deploy pipelines and dashboards](guides/blueprints-deployment.md) | Terraform infrastructure with Blueprints previews, staging, and production releases |

## Example usage

Supply a MotherDuck read-write token as `MOTHERDUCK_TOKEN` through your secret
manager or shell environment. This configuration does not need an organization
admin token.

```terraform
terraform {
  required_version = ">= 1.5.0"

  required_providers {
    motherduck = {
      source  = "motherduckdb/motherduck"
      version = "~> 0.2.2"
    }
  }
}

provider "motherduck" {}

resource "motherduck_database" "analytics" {
  name                    = "analytics"
  snapshot_retention_days = 7
}
```

Run `terraform init`, `terraform plan`, and `terraform apply`. Terraform installs
the provider from the [Registry](https://registry.terraform.io/providers/motherduckdb/motherduck)
and verifies its publisher signature. Commit `.terraform.lock.hcl` with your
configuration.

The database is owned by the account behind `MOTHERDUCK_TOKEN`. For automation,
use a dedicated service account. Do not set the provider's `database` option to
a database being created in the same apply.

## What Terraform manages

| Area | Resources | Typical use |
| --- | --- | --- |
| Warehouse structure | [Database](resources/database.md), [schema](resources/schema.md), [table](resources/table.md), [view](resources/view.md) | Provision landing tables and SQL models |
| Identity and compute | [Service account](resources/service_account.md), [access token](resources/access_token.md), [Duckling configuration](resources/duckling_config.md) | Separate pipelines, environments, and reader workloads |
| Data access | [Share](resources/share.md), [share grant](resources/share_grant.md), [role](resources/role.md), [role grant](resources/role_grant.md) | Publish curated data to accounts and teams |
| Storage access and recovery | [Secret](resources/secret.md), [snapshot](resources/snapshot.md) | Configure external storage access and named recovery points |
| Optional application definitions | [Flight](resources/flight.md), [Dive](resources/dive.md), [Guide](resources/guide.md) | Manage code and content only when Terraform is their sole deployment owner |

Terraform provisions structure. Your ingestion or transformation pipeline loads
and refreshes data. For Python Flights, Dive dashboards, and Guide content, the
recommended application deployment path is [MotherDuck Blueprints](guides/blueprints-deployment.md).
Dives and Guides are experimental provider resources. Flight-run resources are
deprecated. See [resource scope and migration](guides/resource-scope.md).

Data sources inspect existing objects without taking ownership. For example,
[current user](data-sources/current_user.md) checks the SQL identity and
[owned share](data-sources/owned_share.md) reads a share published by another
deployment workflow.

## Authentication

| Environment variable | Used for |
| --- | --- |
| `MOTHERDUCK_TOKEN` | SQL resources, including databases, shares, roles, and catalog reads |
| `MOTHERDUCK_ADMIN_TOKEN` | REST administration, including service accounts, tokens, and compute settings |

Supply only the credentials required by that Terraform root. A SQL token and an
organization admin token are separate credentials. Bootstrap identities first,
then run warehouse Terraform using the resulting writer token.
See [authentication and account ownership](guides/authentication.md).

## Before using production data

- Use separate accounts and state for independent environments. Two tokens for
  the same account do not isolate its data.
- Read the resource's lifecycle notes before changing names or table columns.
  A table schema change replaces the table.
- Store state and saved plans in an encrypted, access-controlled backend.
  `sensitive` masks output, but secrets can still be stored in state.
- Assign each object to one deployment tool. Terraform and dbt or Blueprints
  should not both manage the same definition.

See [state and imports](guides/state-and-lifecycle.md),
[Terraform best practices](guides/terraform-best-practices.md), and
[sharing and read scaling](guides/sharing-and-read-scaling.md).

## Compatibility and support

Terraform 1.5 or later is supported. Ephemeral resources require Terraform 1.10
or later. Release packages are available for Linux and macOS on amd64 and arm64.
Windows is not a supported release target.

Service features still depend on your MotherDuck account, permissions, and
region. See the [tested CLI matrix](guides/ci-and-release.md) and
[release notes](https://github.com/motherduckdb/terraform-provider-motherduck/releases).
For provider issues, include a minimal configuration and redacted diagnostics in
a [GitHub issue](https://github.com/motherduckdb/terraform-provider-motherduck/issues).

<!-- schema generated by tfplugindocs -->
## Schema

### Optional

- `admin_token` (String, Sensitive) MotherDuck organization admin token for REST/control-plane operations. Defaults to `MOTHERDUCK_ADMIN_TOKEN`.
- `api_base_url` (String) MotherDuck REST API base URL. Must be an absolute HTTP or HTTPS URL with a host. Defaults to `https://api.motherduck.com`.
- `attach_mode` (String) Optional MotherDuck attach mode. Supported values are `workspace` and `single`. Use `single` with `database` to attach that existing database without attaching other workspace databases; DuckDB/MotherDuck system catalogs such as `memory` and `md_information_schema` can still be present. Omit this argument for MotherDuck's default workspace attachment behavior.
- `custom_user_agent` (String) Optional custom user agent suffix sent on both the DuckDB/MotherDuck SQL connection and MotherDuck REST API requests.
- `database` (String) Optional MotherDuck database to attach during provider SQL initialization.
- `request_timeout_seconds` (Number) Optional timeout, in seconds, for MotherDuck REST API requests. Defaults to 30 seconds.
- `token` (String, Sensitive) MotherDuck token for SQL/data-plane operations. Defaults to `MOTHERDUCK_TOKEN`.
