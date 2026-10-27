---
page_title: "Hypertenancy blueprint (legacy)"
subcategory: "Reusable blueprints"
description: |-
  Legacy tenant module. Use the read hypertenancy blueprint for new deployments.
---

# Hypertenancy blueprint (legacy)

For new deployments, use [writer bootstrap](blueprint-writer-bootstrap.md)
followed by [read hypertenancy](blueprint-read-hypertenancy.md).

The `examples/blueprints/hypertenancy` module creates the same per-tenant
resources as `read-hypertenancy`: a database, an `app` schema, a restricted
share and grant, a reader service account with Duckling configuration, a
one-hour setup token, and rotating read-scaling tokens. It has the same inputs
and outputs except `expected_writer_username`, the guard that fails the plan
when the SQL token does not belong to the writer. The module stays available
so existing module sources pinned to it keep working.

## Switch to read hypertenancy

The resource addresses inside both modules are identical, so switching does
not replace tenant resources:

1. Change the module `source` from `.../blueprints/hypertenancy?ref=<tag>` to
   `.../blueprints/read-hypertenancy?ref=<tag>`. Keep the module block name, or
   add a `moved` block from `module.hypertenancy` to the new name.
2. Set `expected_writer_username` to the service account behind
   `MOTHERDUCK_TOKEN`.
3. Run `terraform init -upgrade` and confirm that `terraform plan` creates or
   destroys nothing.

Token rotation, reader setup, and tenant offboarding work the same way in both
modules. See [read hypertenancy](blueprint-read-hypertenancy.md).
