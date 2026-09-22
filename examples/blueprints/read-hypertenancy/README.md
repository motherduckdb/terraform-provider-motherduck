# Read Hypertenancy Blueprint

Creates one tenant database, restricted share, reader service account, and reader token per tenant, with all tenant databases and shares owned by a single writer identity.

## Why The Writer Must Run This Module

MotherDuck databases are writable only by the identity that owns them, and only the owner of a share can run `GRANT READ ON SHARE`. So the writer service account cannot be a bystander that Terraform merely creates: the provider's `MOTHERDUCK_TOKEN` for this module must **be** the writer's token. Everything the module creates (tenant databases, schemas, shares, grants) is then owned by the writer, which is exactly what lets the ingestion pipeline write and the reader accounts read.

Deployment is therefore two stages, each with its own state:

1. **Bootstrap** (admin credentials): the [writer-bootstrap blueprint](../writer-bootstrap) creates the writer service account and read-write token. Store the token in a secret manager.
2. **Data plane** (this module): run with `MOTHERDUCK_TOKEN` set to the writer token from stage one, plus `MOTHERDUCK_ADMIN_TOKEN` for the REST-backed reader service accounts and tokens.

## Requirements

- `MOTHERDUCK_TOKEN`: the **writer service account's** token, minted by the writer-bootstrap stage.
- `MOTHERDUCK_ADMIN_TOKEN` from an organization admin, for reader service accounts and access tokens.

## Quick Start

Stage one (see [writer-bootstrap](../writer-bootstrap) for the full example):

```bash
export MOTHERDUCK_ADMIN_TOKEN=...
terraform -chdir=bootstrap apply       # creates svc_writer_prod and its token
# store the writer_token output in your secret manager
```

Stage two uses a separate root and state. The module source is pinned to
v0.2.11, which includes reader setup and overlapping token rotation.

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

module "read_hypertenancy" {
  source = "github.com/motherduckdb/terraform-provider-motherduck//examples/blueprints/read-hypertenancy?ref=v0.2.11"

  expected_writer_username = "svc_writer_prod"

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
  value       = module.read_hypertenancy.tenants
}
```

```bash
export MOTHERDUCK_TOKEN=...        # the writer token from stage one
export MOTHERDUCK_ADMIN_TOKEN=...
terraform init
terraform plan -out=tfplan
terraform apply tfplan
```

`expected_writer_username` is an optional guard that fails the plan when the SQL session identity does not match the writer, so tenant databases are never accidentally created under a personal or CI identity that the pipeline cannot write through. Leave it unset if your session reports a generic identity such as `duckdb`.

Key each tenant by a stable internal tenant id, not by an email or company name. Renaming a tenant key replaces every resource for that tenant.

## Naming

Keep `database_prefix`, `share_prefix`, and `reader_prefix` simple: they must start with an ASCII letter and use only ASCII letters, digits, and underscores. Tenant keys and optional `slug` values are normalized into lowercase alphanumeric/underscore suffixes before names are generated, and normalized slugs must stay unique across tenants.

## Reader Setup And Tokens

The writer token has read-write scope over all tenant databases. Distribute it only to the ingestion or modeling runtime, never to tenant-facing services. Generated reader tokens are sensitive Terraform state: move them into the appropriate runtime secret managers immediately, restrict access to the backend that stores this module's state, and do not print them in CI logs.

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
