---
page_title: "Writer bootstrap blueprint"
subcategory: "Reusable blueprints"
description: |-
  Create the writer service account and rotating read-write tokens that own tenant data.
---

# Writer bootstrap blueprint

This blueprint creates the writer service account and read-write token that own tenant data infrastructure. It is stage one for the hypertenancy and read-hypertenancy blueprints: MotherDuck databases are writable only by the identity that owns them, and only the owner of a share can `GRANT READ ON SHARE`, so tenant data planes should be applied as a dedicated writer identity instead of a personal token.

## Terraform shape

One `motherduck_service_account`, one legacy `motherduck_access_token` with `token_type = "read_write"`, and one additional read-write token per entry in `writer_token_generations`. The example implementation is in `examples/blueprints/writer-bootstrap`.

This blueprint is REST-only and requires an organization-admin `MOTHERDUCK_ADMIN_TOKEN`. No SQL token is needed.

## Operating model

Apply this module from an admin-owned root module with its own state, separate from tenant data state. The workflow holds organization-admin credentials and produces a read-write token, so it deserves tighter access than routine tenant changes.

Store the generated writer token in a secret manager immediately. Downstream data-plane Terraform (for example the read-hypertenancy blueprint) reads it from there and uses it as `MOTHERDUCK_TOKEN`, which makes the writer the owner of every tenant database and share it creates.

## Token lifecycle

The writer token expires after `writer_token_ttl_seconds` (30 days by default) and Terraform does not rotate it automatically. Do not rotate it with `terraform apply -replace`. Replacement revokes the current token in the same apply, which breaks the data-plane Terraform and the ingestion runtime until they receive the new value.

Rotate with an overlapping generation instead:

1. Add a generation and apply. The module creates a second read-write token and keeps the current one.

   ```hcl
   writer_token_generations = ["2026_10"]
   ```

2. Copy `writer_rotation_tokens["2026_10"]` into the secret manager and verify the data-plane Terraform and ingestion connections with it.
3. Set `retire_legacy_writer_token = true`, keep the same generation, and apply again. The module revokes the original token. The `writer_token` output becomes null and `writer_rotation_tokens` remains available.

Retirement fails the plan unless MotherDuck already lists a token for a named generation. Terraform cannot verify that every consumer has switched, so confirm that before step 3. Keep the generation managed until the next rotation completes.

## Inputs and outputs

Inputs:

| Variable | Description | Default |
| --- | --- | --- |
| `writer_username` | Service account username that will own tenant databases and shares. Must start with an ASCII letter and contain only ASCII letters, digits, and underscores. | required |
| `writer_token_name` | Name for the generated writer access token. | `"terraform-writer"` |
| `writer_token_ttl_seconds` | TTL for each generated writer token, between 300 and 31536000 seconds. | `2592000` (30 days) |
| `writer_token_generations` | Additional read-write token generations to create during an overlapping rotation. Each token is named `<writer_token_name>-<generation>`. | `[]` |
| `retire_legacy_writer_token` | Revoke the original writer token after a replacement generation is live. | `false` |

Outputs:

| Output | Description |
| --- | --- |
| `writer_username` | Writer service account username. |
| `writer_token` | Original writer token (sensitive), or null after retirement. Store it in a secret manager. Downstream data-plane Terraform uses it as `MOTHERDUCK_TOKEN`. |
| `writer_rotation_tokens` | Overlapping writer tokens keyed by generation (sensitive). |

## Example

```hcl
module "writer_bootstrap" {
  source = "github.com/motherduckdb/terraform-provider-motherduck//examples/blueprints/writer-bootstrap?ref=v0.2.13"

  writer_username = "svc_writer_prod"
}

output "writer_token" {
  value     = module.writer_bootstrap.writer_token
  sensitive = true
}

output "writer_rotation_tokens" {
  value     = module.writer_bootstrap.writer_rotation_tokens
  sensitive = true
}
```

Pin the module source to a release tag and keep that ref independent from the provider version constraint.

Continue with [customer-facing analytics](customer-facing-analytics.md) to use
the writer with a complete reader provisioning and attachment workflow.
