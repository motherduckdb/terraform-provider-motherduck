# Hypertenancy Blueprint

Creates one isolated database, schema, restricted share, reader service account, and reader token per tenant.

## Requirements

This module needs both provider credentials in the environment:

- `MOTHERDUCK_TOKEN` for SQL-backed database, schema, share, and share-grant operations.
- `MOTHERDUCK_ADMIN_TOKEN` from an organization admin for reader service accounts and access tokens.

## Quick Start

Call the module from a root module. Pin the module source to a release tag so tenant infrastructure does not change when this repository does:

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
  source = "github.com/motherduckdb/terraform-provider-motherduck//examples/blueprints/hypertenancy?ref=v0.1.1"

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

## Reader Tokens

Generated reader tokens are sensitive Terraform state. Move them into your application secret manager immediately and restrict access to the backend that stores this module's state. Do not print them in CI logs.

Tokens expire after `reader_token_ttl_seconds` (30 days by default) and Terraform does not rotate them automatically. Plan a rotation workflow before the TTL elapses, for example:

```bash
terraform apply -replace='module.hypertenancy.motherduck_access_token.reader["acme"]'
```

## Removing A Tenant

Removing a tenant from `tenants` destroys that tenant's database and all data inside it on the next apply. Snapshot or export tenant data first, and treat tenant removal as a deliberate offboarding step, not routine configuration cleanup.
