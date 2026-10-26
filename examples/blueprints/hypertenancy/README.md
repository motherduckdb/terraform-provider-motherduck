# Hypertenancy Blueprint (Legacy)

For new deployments, use [writer-bootstrap](../writer-bootstrap) followed by
[read-hypertenancy](../read-hypertenancy).

This module creates the same per-tenant database, `app` schema, restricted
share and grant, reader service account, Duckling configuration, setup token,
and rotating read-scaling tokens as `read-hypertenancy`. It lacks only the
`expected_writer_username` guard. It stays here so module sources pinned to it
keep working.

## Switch To Read Hypertenancy

Resource addresses inside both modules are identical, so switching does not
replace tenant resources:

1. Change the module `source` to
   `github.com/motherduckdb/terraform-provider-motherduck//examples/blueprints/read-hypertenancy?ref=v0.3.8`.
   Keep the module block name, or add a `moved` block for the new name.
2. Set `expected_writer_username` to the service account behind
   `MOTHERDUCK_TOKEN`.
3. Run `terraform init -upgrade` and confirm that `terraform plan` creates or
   destroys nothing.

Inputs, outputs, reader setup, token rotation, and tenant removal are documented
in the [read-hypertenancy README](../read-hypertenancy/README.md).
