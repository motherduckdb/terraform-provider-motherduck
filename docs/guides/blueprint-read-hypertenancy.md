---
page_title: "Read hypertenancy with centralized writes"
subcategory: "Reusable blueprints"
description: |-
  Own every tenant database and share with one writer and isolate reads per tenant.
---

# Read hypertenancy with centralized writes

This blueprint gives every tenant its own database, restricted share, reader
service account, and read-scaling token. One writer service account owns every
tenant database and share. Readers only query their own tenant's share. Use it
for new multi-tenant deployments.

## Architecture

![A pipeline writes Acme and Globex data through one central writer that owns both tenant databases and their restricted shares. Each share is granted to and attached by its own tenant reader account with a separate read pool. The backend authenticates users and routes queries to the matching reader.](https://raw.githubusercontent.com/motherduckdb/terraform-provider-motherduck/main/docs/assets/customer-facing-analytics.png)

## Why the writer applies the data plane

Only the identity that owns a database can write to it, and only the owner of
a share can run `GRANT READ ON SHARE`. MotherDuck has no database-level grant
and no write grant. The data plane must therefore be applied with the writer's
token, so the writer owns every tenant database and share.

Deploy in two stages, each with its own state:

1. **Bootstrap** (`examples/blueprints/writer-bootstrap`): an organization
   admin applies it with `MOTHERDUCK_ADMIN_TOKEN` to create the writer service
   account and its read-write token. Store the token in a secret manager.
2. **Data plane** (`examples/blueprints/read-hypertenancy`): apply it with
   `MOTHERDUCK_TOKEN` set to the writer token, plus `MOTHERDUCK_ADMIN_TOKEN` for
   reader service accounts, Duckling configuration, and tokens.

Set `expected_writer_username` so the plan fails when the SQL token belongs to
a personal or CI identity instead of the writer.

## Terraform shape

The module uses `for_each` over a tenant map. Each tenant gets a
`motherduck_database`, an `app` `motherduck_schema`, a
`motherduck_service_account`, a `motherduck_duckling_config`, a restricted
`motherduck_share`, a `motherduck_share_grant`, and `motherduck_access_token`
resources: a one-hour setup token, a read-scaling token, and one read-scaling
token per rotation generation.

`database_prefix`, `share_prefix`, and `reader_prefix` must start with an ASCII
letter and contain only ASCII letters, digits, and underscores. Tenant keys and
optional slugs are normalized into lowercase alphanumeric and underscore
suffixes, and normalized slugs must be unique across tenants.

## Operating model

Use this model when:

- writes should go through one controlled identity.
- tenant data must be restorable, shareable, or dropped independently.
- tenants need separate share grants, reader credentials, and read compute.

Tradeoffs:

- the writer token can write every tenant database. Give it only to the
  ingestion runtime and the data-plane Terraform runtime, never to a
  tenant-facing service.
- more MotherDuck objects and Terraform state entries per tenant.
- schema migrations must loop over every tenant database.

Give each reader token only to the serving layer for that tenant. When many
small tenants share one schema and do not need an independent lifecycle, a
pooled table with [secure views](row-and-column-security.md) can replace the
per-tenant databases. Compare the two in
[choose a tenancy model](customer-facing-analytics.md#choose-a-tenancy-model).

## Token lifecycle and offboarding

Generated reader tokens expire after `reader_token_ttl_seconds` (30 days by default) and Terraform does not rotate them automatically. Do not rotate them with `terraform apply -replace`. Replacement revokes the current token in the same apply, so the tenant backend loses access until it receives the new value.

Rotate before the TTL elapses with an overlapping generation:

1. Add a generation and apply. Every tenant reader gets a second read-scaling token named `terraform-reader-<generation>`.

   ```hcl
   reader_token_generations = ["2026_10"]
   ```

2. Move `module.read_hypertenancy.reader_rotation_tokens["acme/2026_10"]` (keyed by `<tenant_id>/<generation>`) into each backend secret and verify new connections.
3. Set `retire_legacy_reader_token = true`, keep the same generation, and apply again. The module revokes the original `reader_tokens`.

Retirement fails the plan unless MotherDuck already lists a named replacement generation for each reader. Keep the generation managed until the next rotation completes.

Rotate the writer token with the writer-bootstrap generations workflow and update the data-plane Terraform credentials in the same window.

Removing a tenant from the `tenants` map destroys that tenant's database and all data inside it on the next apply. Snapshot or export tenant data first and treat tenant removal as deliberate offboarding.

## Inputs and outputs

Inputs:

| Variable | Description | Default |
| --- | --- | --- |
| `expected_writer_username` | Optional guard: fail the plan when the provider's SQL identity does not match this writer service account username. Leave null to skip the check. | `null` |
| `database_prefix` | Prefix for tenant database names. | `"tenant"` |
| `reader_prefix` | Prefix for tenant reader service account usernames. | `"svc_reader"` |
| `share_prefix` | Prefix for tenant share names. | `"share"` |
| `reader_token_ttl_seconds` | TTL for generated tenant reader tokens, between 300 and 31536000 seconds. | `2592000` (30 days) |
| `reader_token_generations` | Additional read-scaling token generations to create for every reader during an overlapping rotation. | `[]` |
| `retire_legacy_reader_token` | Revoke the original `terraform-reader` tokens after a replacement generation is live. | `false` |
| `tenants` | Tenant definitions keyed by stable tenant id. Each value accepts optional `display_name`, `slug`, and `snapshot_retention_days` (default `7`). Normalized slugs must be unique across tenants. | required |

Outputs:

| Output | Description |
| --- | --- |
| `tenants` | Per-tenant summary keyed by tenant id: display name, database, share, and reader username. |
| `tenant_databases` | Tenant database names keyed by tenant id. |
| `tenant_shares` | Tenant share names keyed by tenant id. |
| `reader_usernames` | Reader service account usernames keyed by tenant id. |
| `reader_tokens` | Original read-scaling reader tokens keyed by tenant id (sensitive). Empty after retirement. |
| `reader_rotation_tokens` | Overlapping read-scaling tokens keyed by `<tenant_id>/<generation>` (sensitive). |
| `share_urls` | Restricted share URLs keyed by tenant id (sensitive). Each reader attaches its share once. |
| `reader_setup_tokens` | One-hour read-write tokens keyed by tenant id (sensitive), used only for the initial share attachment. |

## Example

Stage one creates the writer (see the writer-bootstrap blueprint), and its token becomes `MOTHERDUCK_TOKEN` for stage two:

```hcl
module "writer_bootstrap" {
  source = "github.com/motherduckdb/terraform-provider-motherduck//examples/blueprints/writer-bootstrap?ref=v0.3.8"

  writer_username = "svc_writer_prod"
}
```

Stage two, in a separate root module and state. Key each tenant by a stable internal tenant id, not by an email or company name. Renaming a tenant key replaces every resource for that tenant. Pin the module source to a release tag when consuming this blueprint from another repository.

```hcl
module "read_hypertenancy" {
  source = "github.com/motherduckdb/terraform-provider-motherduck//examples/blueprints/read-hypertenancy?ref=v0.3.8"

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

output "share_urls" {
  value     = module.read_hypertenancy.share_urls
  sensitive = true
}

output "reader_setup_tokens" {
  value     = module.read_hypertenancy.reader_setup_tokens
  sensitive = true
}

output "reader_tokens" {
  value     = module.read_hypertenancy.reader_tokens
  sensitive = true
}

output "reader_rotation_tokens" {
  value     = module.read_hypertenancy.reader_rotation_tokens
  sensitive = true
}
```

## Complete the reader setup

The module grants each reader its share and configures each reader's Duckling
(Standard instances, one read-scaling replica, 60-second cooldowns). It does not
attach the share as the reader. Attach each share once, connected with that
tenant's `reader_setup_tokens` value:

```sql
ATTACH '<tenant share_urls value>' AS reporting;
```

The attachment is stored for the reader account. The setup token lasts one
hour. Never give it to the serving application. Serve queries with the
read-scaling reader token after the writer pipeline publishes data. See
[sharing and read scaling](sharing-and-read-scaling.md) for freshness and
replica behavior.

The shares omit `update_mode` and adopt the service default. If the resulting
share uses manual publication, the writer pipeline must run `UPDATE SHARE`
after loading data. A no-change apply does not publish new rows.

For a complete application with a backend that routes tenant queries, use
[customer-facing analytics](customer-facing-analytics.md).
