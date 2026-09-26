# Choose an example

Use a runnable root for a complete Terraform deployment, a module when composing
your own root, or a reference snippet when looking up one provider surface.
Terraform 1.5 or later is supported. Ephemeral resources require Terraform 1.10.

| Goal | Start here | Credentials | Result and next step |
| --- | --- | --- | --- |
| Learn the first warehouse | [Simple warehouse](warehouses/simple/README.md) | SQL writer | Five resources and sample revenue, then [dbt](cookbook-pipeline/README.md) |
| Separate dev and prod | [Warehouse bootstrap](warehouses/bootstrap/README.md) and [layered warehouse](warehouses/layered/README.md) | Admin for identities, matching writer for data | Separate owners/states, then ingestion and modeling |
| Run dlt and dbt over provisioned infrastructure | [Cookbook pipeline](cookbook-pipeline/README.md) | Existing SQL writer | Repeatable ingest and tested runtime-owned models |
| Serve tenant usage analytics | [Customer-facing analytics](customer-facing-analytics/README.md) and [backend](customer-facing-analytics/backend/README.md) | Writer plus admin, reader tokens only in backend | Tenant-isolated data and authenticated queries |
| Compose reusable tenant infrastructure | [Writer bootstrap](blueprints/writer-bootstrap/README.md), then [read hypertenancy](blueprints/read-hypertenancy/README.md) | Admin bootstrap, writer plus admin for tenants | Explicit writer identity and reader attachment |
| Use the lighter legacy tenant module | [Hypertenancy](blueprints/hypertenancy/README.md) | Writer plus admin | Similar resources without the writer identity precondition |
| Manage team access | [Access control](access-control/README.md) | SQL role administrator and share owner | Roles, memberships and share grants |
| Audit access outside state | [Share audit](share-access-audit/README.md) and [role audit](role-access-audit/README.md) | SQL account able to inspect the objects | Read-only differences against explicit policy |
| Use Pulumi Python | [Pulumi](pulumi/python/README.md) | Existing SQL writer | Database, schema and table through the pinned bridge |
| Look up one API surface | [Resources](resources), [data sources](data-sources), [provider](provider), [ephemeral resources](ephemeral-resources) | Depends on the surface | Minimal snippets, with schema and prerequisites in the Registry |

Install the provider with `terraform init` from the Terraform Registry. A
filesystem mirror is optional for offline installation. Copy every file in a
runnable root, including SQL templates. For modules, pin the Git source to a
reviewed release tag such as `v0.3.0` and pass providers from your root. The module source ref
and the provider version constraint are independent.

Check [authentication](../docs/guides/authentication.md) before applying. A
service-account resource does not switch the provider's SQL identity. Keep
bootstrap state protected and transfer credentials through a secret manager.
Do not give runtime applications access to the full Terraform state.

Each workflow describes its build, plan, apply, no-op check and teardown. Use
unique disposable names for learning. Remove data and consumers before their
owner account. A production offboarding decision is not the same as removing a
tenant key from configuration.

The [MotherDuck cookbook](https://motherduck.com/docs/cookbook/) owns the runtime
recipes linked from these examples. Do not let Terraform and a cookbook job
both manage the same table or share. Native storage remains the default here.
