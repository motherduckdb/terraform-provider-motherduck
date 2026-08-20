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

## Example

```hcl
module "writer_bootstrap" {
  source = "github.com/motherduckdb/terraform-provider-motherduck//examples/blueprints/writer-bootstrap?ref=v0.1.1"

  writer_username = "svc_writer_prod"
}

output "writer_token" {
  value     = module.writer_bootstrap.writer_token
  sensitive = true
}
```
