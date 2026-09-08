---
page_title: "Read Hypertenancy With Centralized Writes"
subcategory: "Reusable blueprints"
---

# Blueprint: Read Hypertenancy With Centralized Writes

This blueprint centralizes writes through one writer identity while isolating reads by tenant database and restricted shares. The writer service account owns every tenant database and share. Reader identities only consume their tenant share.

## Architecture

![A central writer publishes a restricted share per tenant to separate readers.](https://raw.githubusercontent.com/motherduckdb/terraform-provider-motherduck/main/docs/assets/customer-facing-analytics.png)

## Grant Semantics

Two MotherDuck rules shape this blueprint. A database is writable only by the identity that owns it, and access is granted through shares: `GRANT READ ON SHARE <share> TO <username>` can be run only by the owner of the share. There is no database-level grant and no write grant.

So the writer cannot be an identity Terraform merely creates on the side. The tenant data plane must be applied *as* the writer, making the writer the owner of every tenant database (so the pipeline can write) and every share (so the reader grants are permitted).

## Terraform Shape

Deploy in two stages, each with its own state:

1. **Bootstrap** (`examples/blueprints/writer-bootstrap`): an organization admin applies this with `MOTHERDUCK_ADMIN_TOKEN` to create the writer service account and read-write token. The token goes into a secret manager.
2. **Data plane** (`examples/blueprints/read-hypertenancy`): applied with `MOTHERDUCK_TOKEN` set to the writer token from stage one, plus `MOTHERDUCK_ADMIN_TOKEN` for REST-backed reader service accounts and tokens. Use `for_each` for tenant databases, shares, and reader identities.

The data-plane module accepts an optional `expected_writer_username` guard that fails the plan when the SQL session identity does not match the writer, so tenant databases are never accidentally created under a personal or CI identity.

Generated names are intentionally conservative: `database_prefix`, `share_prefix`, and `reader_prefix` must start with an ASCII letter and contain only ASCII letters, digits, and underscores. Tenant keys and optional slugs are normalized into lowercase alphanumeric/underscore suffixes so generated names stay importable and shell-friendly, and normalized slugs must stay unique across tenants.

## Operating Model

Use this model when:

- writes should be serialized through one controlled identity.
- read workloads should be isolated by tenant.
- tenants need separate share grants and reader credentials.
- data pipelines need a predictable list of tenant databases to update.

Tradeoffs:

- writer credentials have broad write access and need stricter handling.
- Terraform for the data plane runs as the writer, so its credential management is coupled to the pipeline identity.
- application teams should not use writer credentials for reads.

## Write Path

The writer token belongs in the ingestion or modeling runtime secret manager and in the data-plane Terraform runtime. Terraform creates tenant databases as the writer. The runtime job performs actual data writes with the same identity.

## Read Path

Each tenant receives a reader service account and token. The token should be distributed only to that tenant's serving layer. The tenant application reads through the restricted share instead of connecting as the writer.

## Token Lifecycle And Offboarding

Generated writer and reader tokens expire after their configured TTLs (`writer_token_ttl_seconds` in the writer-bootstrap blueprint and `reader_token_ttl_seconds` here, 30 days by default) and Terraform does not rotate them automatically. Plan a rotation workflow, for example `terraform apply -replace='module.read_hypertenancy.motherduck_access_token.reader["acme"]'`, before the TTL elapses. Rotating the writer token also means updating the data-plane Terraform credentials.

Removing a tenant from the `tenants` map destroys that tenant's database and all data inside it on the next apply. Snapshot or export tenant data first and treat tenant removal as deliberate offboarding.

## Inputs And Outputs

Inputs:

| Variable | Description | Default |
| --- | --- | --- |
| `expected_writer_username` | Optional guard: fail the plan when the provider's SQL identity does not match this writer service account username. Leave null to skip the check. | `null` |
| `database_prefix` | Prefix for tenant database names. | `"tenant"` |
| `reader_prefix` | Prefix for tenant reader service account usernames. | `"svc_reader"` |
| `share_prefix` | Prefix for tenant share names. | `"share"` |
| `reader_token_ttl_seconds` | TTL for generated tenant reader tokens, between 300 and 31536000 seconds. | `2592000` (30 days) |
| `tenants` | Tenant definitions keyed by stable tenant id. Each value accepts optional `display_name`, `slug`, and `snapshot_retention_days` (default `7`). Normalized slugs must be unique across tenants. | required |

Outputs:

| Output | Description |
| --- | --- |
| `tenants` | Per-tenant summary keyed by tenant id: display name, database, share, and reader username. |
| `tenant_databases` | Tenant database names keyed by tenant id. |
| `tenant_shares` | Tenant share names keyed by tenant id. |
| `reader_usernames` | Reader service account usernames keyed by tenant id. |
| `reader_tokens` | Generated reader tokens keyed by tenant id (sensitive). Store these in a secret manager. |

## Example

Stage one creates the writer (see the writer-bootstrap blueprint), and its token becomes `MOTHERDUCK_TOKEN` for stage two:

```hcl
module "writer_bootstrap" {
  source = "github.com/motherduckdb/terraform-provider-motherduck//examples/blueprints/writer-bootstrap?ref=v0.2.2"

  writer_username = "svc_writer_prod"
}
```

Stage two, in a separate root module and state. Key each tenant by a stable internal tenant id, not by an email or company name. Renaming a tenant key replaces every resource for that tenant. Pin the module source to a release tag when consuming this blueprint from another repository.

```hcl
module "read_hypertenancy" {
  source = "github.com/motherduckdb/terraform-provider-motherduck//examples/blueprints/read-hypertenancy?ref=v0.2.2"

  expected_writer_username = "svc_writer_prod"

  tenants = {
    acme = {
      display_name = "Acme"
    }
    globex = {
      display_name = "Globex"
    }
  }
}

output "tenants" {
  value = module.read_hypertenancy.tenants
}
```

## Complete the reader setup

The module creates grants, but does not attach shares as the readers. Reader
accounts need an initial read-write connection and a share attachment before
using their read-scaling tokens. The module also leaves compute settings to
the caller. See [sharing and read scaling](sharing-and-read-scaling.md).

For a complete example with reader compute, temporary setup credentials, and
automatic publication, use [customer-facing analytics](customer-facing-analytics.md).

These older modules omit `update_mode` and adopt the service default. If the
resulting share uses manual publication, the writer pipeline must run
`UPDATE SHARE` after loading data. A no-change apply does not publish new rows.
