---
page_title: "Writer Bootstrap"
subcategory: "Reusable blueprints"
---

# Blueprint: Writer Bootstrap

This blueprint creates the writer service account and read-write token that own tenant data infrastructure. It is stage one for the hypertenancy and read-hypertenancy blueprints: MotherDuck databases are writable only by the identity that owns them, and only the owner of a share can `GRANT READ ON SHARE`, so tenant data planes should be applied as a dedicated writer identity instead of a personal token.

## Terraform Shape

One `motherduck_service_account` and one `motherduck_access_token` with `token_type = "read_write"`. The example implementation is in `examples/blueprints/writer-bootstrap`.

This blueprint is REST-only and requires an organization-admin `MOTHERDUCK_ADMIN_TOKEN`. No SQL token is needed.

## Operating Model

Apply this module from an admin-owned root module with its own state, separate from tenant data state. The workflow holds organization-admin credentials and produces a read-write token, so it deserves tighter access than routine tenant changes.

Store the generated writer token in a secret manager immediately. Downstream data-plane Terraform (for example the read-hypertenancy blueprint) reads it from there and uses it as `MOTHERDUCK_TOKEN`, which makes the writer the owner of every tenant database and share it creates.

## Token Lifecycle

The writer token expires after `writer_token_ttl_seconds` (30 days by default) and Terraform does not rotate it automatically. Rotate with `terraform apply -replace=module.writer_bootstrap.motherduck_access_token.writer`, update the secret manager, and refresh the credentials used by the data-plane Terraform and the ingestion runtime.

## Inputs And Outputs

Inputs:

| Variable | Description | Default |
| --- | --- | --- |
| `writer_username` | Service account username that will own tenant databases and shares. Must start with an ASCII letter and contain only ASCII letters, digits, and underscores. | required |
| `writer_token_name` | Name for the generated writer access token. | `"terraform-writer"` |
| `writer_token_ttl_seconds` | TTL for the generated writer token, between 300 and 31536000 seconds. | `2592000` (30 days) |

Outputs:

| Output | Description |
| --- | --- |
| `writer_username` | Writer service account username. |
| `writer_token` | Generated writer token (sensitive). Store it in a secret manager. Downstream data-plane Terraform uses it as `MOTHERDUCK_TOKEN`. |

## Example

```hcl
module "writer_bootstrap" {
  source = "github.com/motherduckdb/terraform-provider-motherduck//examples/blueprints/writer-bootstrap?ref=v0.2.2"

  writer_username = "svc_writer_prod"
}

output "writer_token" {
  value     = module.writer_bootstrap.writer_token
  sensitive = true
}
```

Continue with [customer-facing analytics](customer-facing-analytics.md) to use
the writer with a complete reader provisioning and attachment workflow.
