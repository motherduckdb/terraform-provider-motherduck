# Writer Bootstrap Blueprint

Creates the writer service account and read-write token that own tenant data infrastructure. This is stage one of the two-stage read-hypertenancy deployment: MotherDuck databases are writable only by the identity that owns them, and only the owner of a share can `GRANT READ ON SHARE`, so the tenant data plane must be applied *as* the writer.

## Requirements

- `MOTHERDUCK_ADMIN_TOKEN` from an organization admin. No SQL token is needed. This module only calls REST administration APIs.

## Quick Start

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

module "writer_bootstrap" {
  source = "github.com/motherduckdb/terraform-provider-motherduck//examples/blueprints/writer-bootstrap?ref=v0.2.11"

  writer_username = "svc_writer_prod"
}

output "writer_token" {
  value     = module.writer_bootstrap.writer_token
  sensitive = true
}
```

The module source is pinned to v0.2.11, which includes overlapping token
rotation. Keep the module ref independent from the provider version constraint.

```bash
export MOTHERDUCK_ADMIN_TOKEN=...
terraform init
terraform plan -out=tfplan
terraform apply tfplan
```

Move the writer token into a secret manager immediately. The data-plane stage (see the read-hypertenancy blueprint) reads it from there and uses it as its `MOTHERDUCK_TOKEN`.

## State Guidance

Keep this module in its own state, separate from tenant data state. It holds an organization-admin-scoped workflow and a read-write token. Both deserve tighter access than routine tenant changes.

The writer token expires after `writer_token_ttl_seconds` (30 days by default)
and Terraform does not rotate it automatically. Do not use `-replace` for a
consumer-facing rotation because it revokes the current token in the same
apply. Create an overlapping generation instead:

```hcl
writer_token_generations = ["2026_10"]
```

Apply once, transfer `writer_rotation_tokens["2026_10"]` to the data-plane
secret manager, and verify the reader or ingestion connections. Then apply a
second time with the same generation and:

```hcl
retire_legacy_writer_token = true
```

The module migrates the original `motherduck_access_token.writer` state to its
indexed legacy address and revokes it in that later apply. The `writer_token`
output becomes null, while the rotation output remains available. Keep the
rotation generation managed until the next overlap is complete.
