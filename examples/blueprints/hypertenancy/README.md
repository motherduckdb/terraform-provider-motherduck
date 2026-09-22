# Hypertenancy Blueprint

Creates one isolated database, schema, restricted share, reader service account, and reader token per tenant.

## Requirements

This module needs both provider credentials in the environment:

- `MOTHERDUCK_TOKEN` for SQL-backed database, schema, share, and share-grant operations.
- `MOTHERDUCK_ADMIN_TOKEN` from an organization admin for reader service accounts and access tokens.

## Quick Start

The module source is pinned to v0.2.11, which includes reader setup and
overlapping token rotation. Keep the module ref pinned independently from
the provider version constraint.

```hcl
terraform {
  required_version = ">= 1.5.0"

  required_providers {
    motherduck = {
      source  = "motherduckdb/motherduck"
      version = ">= 0.1.0"
    }
  }
}

provider "motherduck" {}

module "hypertenancy" {
  source = "github.com/motherduckdb/terraform-provider-motherduck//examples/blueprints/hypertenancy?ref=v0.2.11"

  tenants = {
    acme = {
      display_name = "Acme"
    }
    globex = {
      display_name            = "Globex"
      slug                    = "globex_eu"
      snapshot_retention_days = 14
    }
  }
}

output "tenants" {
  description = "Per-tenant databases, shares, and reader usernames."
  value       = module.hypertenancy.tenants
}
```

```bash
export MOTHERDUCK_TOKEN=...
export MOTHERDUCK_ADMIN_TOKEN=...
terraform init
terraform plan -out=tfplan
terraform apply tfplan
```

Key each tenant by a stable internal tenant id, not by an email or company name. Renaming a tenant key replaces every resource for that tenant.

## Naming

Keep `database_prefix`, `share_prefix`, and `reader_prefix` simple: they must start with an ASCII letter and use only ASCII letters, digits, and underscores. Tenant keys and optional `slug` values are normalized into lowercase alphanumeric/underscore suffixes before names are generated, and normalized slugs must stay unique across tenants.

## Writer Identity

The identity behind `MOTHERDUCK_TOKEN` owns every tenant database and share this module creates, which makes it the only identity that can write tenant data and the only one allowed to `GRANT READ ON SHARE` to the readers. Run this module with a dedicated writer service account token (see the [writer-bootstrap blueprint](../writer-bootstrap)) rather than a personal token, so ownership does not depend on an individual's account.

## Reader Setup And Tokens

The `share_urls` and `reader_setup_tokens` outputs are needed once per tenant to
attach the restricted share. Connect as each reader with its setup token and
run:

```sql
ATTACH '<tenant_share_url>' AS reporting;
SELECT table_name
FROM information_schema.tables
WHERE table_catalog = 'reporting' AND table_schema = 'app';
```

Setup tokens last one hour and remain managed by this privileged Terraform root.
A later apply can recreate one after expiration. Never give a setup token to
the serving application. Retiring a legacy reader token does not remove this
independent initialization credential.

The module creates the `app` schema but no tables, so the listing is empty until
your writer pipeline creates them. Query those concrete relations with the
reader token after publication and replica synchronization.

The attachment is stored for that reader account. Move the setup tokens, share
URLs, and reader tokens into protected secret automation immediately. Do not
print them in CI logs.

This module also manages each reader's read-scaling pool at one Standard replica
with a 60-second cooldown. Review the plan before adopting an existing reader
whose compute settings differ from those defaults.

The legacy reader tokens expire after `reader_token_ttl_seconds` (30 days by
default). Terraform does not rotate them automatically. Use overlapping
generations before the TTL elapses:

```hcl
reader_token_generations       = ["2026_10"]
retire_legacy_reader_token     = false
```

Apply once, transfer `reader_rotation_tokens["acme/2026_10"]` to the backend,
and verify new connections. Then apply again with the same generation and
`retire_legacy_reader_token = true` to revoke the legacy `reader_tokens`
generation. Keep the generation until every consumer has moved.

## Removing A Tenant

Removing a tenant from `tenants` destroys that tenant's database and all data inside it on the next apply. Snapshot or export tenant data first, and treat tenant removal as a deliberate offboarding step, not routine configuration cleanup.
