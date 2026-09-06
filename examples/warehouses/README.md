# Warehouse examples

Three independent Terraform roots demonstrate environment ownership and two warehouse sizes.

| Root | Purpose | Credential |
| --- | --- | --- |
| [bootstrap](bootstrap/README.md) | Create dev/prod writer and BI service accounts | MOTHERDUCK_ADMIN_TOKEN |
| [simple](simple/README.md) | One database, raw orders, daily revenue view | Environment writer's MOTHERDUCK_TOKEN |
| [layered](layered/README.md) | Raw/transform/marts databases and a restricted marts share | Environment writer's MOTHERDUCK_TOKEN |

These follow MotherDuck's [resource-management](https://motherduck.com/docs/concepts/resource-management)
and [environment-management](https://motherduck.com/docs/key-tasks/data-warehousing/environment-management)
guidance, retrieved through Context7. Layout follows
[Terraform's standard structure](https://developer.hashicorp.com/terraform/language/modules/develop/structure).
Like the [Databricks examples repository](https://github.com/databricks/terraform-databricks-examples),
each directory explains its prerequisites, deployment, and cleanup.

## Environment boundary

An environment is represented by its **service-account identity, token, compute,
and owned databases**. The environment variable here only labels names.
Changing `environment = "prod"` while using a dev token does not create a
production ownership boundary. Terraform workspaces alone do not switch identity.

```text
admin bootstrap state
  ├── dev_writer  ── owns dev warehouse ── restricted marts share ── dev_bi
  └── prod_writer ── owns prod warehouse ─ restricted marts share ── prod_bi
```

Both writers own all layers of their respective environment in these examples.
They are not separate raw/transform writers; shared databases are read-only.
Service accounts are an isolation boundary within an organization, not separate
organizations or protection against privileged administrators.

## Deployment order

1. Install the [GitHub release through a filesystem mirror](../../docs/guides/github-installation.md).
2. Pick a unique account/database prefix. Apply bootstrap with admin credentials.
3. Transfer generated writer and BI tokens from the protected bootstrap state
   into separate secret-manager entries through approved secret automation.
   Do not print tokens, commit them, or expose bootstrap state to BI users.
4. Copy the chosen warehouse root, including SQL templates, into separate
   working directories for dev and prod. Keep their backend states and state
   access controls separate. Use your organization's encrypted, locking remote
   backend before production use; these copyable examples leave backend choice
   to the caller and otherwise use Terraform's local state.
5. In each job, inject that environment's writer token as `MOTHERDUCK_TOKEN`.
   Do not inject admin credentials into the warehouse job. Check `SELECT md_user()`
   through the authenticated SQL client before apply and compare it with the
   intended bootstrap writer username.
6. Apply dev first, validate data and access, then apply the same reviewed code
   to prod with the prod token, state, and variables. Promote code, not dev state.
   Do not copy production data into dev automatically.

Do not configure a provider using a token resource created in the same apply.
Provider configuration precedes resource creation. The two-phase bootstrap
boundary is intentional.

## Lifecycle and operation boundaries

Terraform owns database, schema, table, view, and share definitions. Ingestion
and data refresh are separate SQL/pipeline operations; these examples create no
Flight runs, Dives, or Guides. A fresh apply creates an empty warehouse.

The table resource uses replacement for schema changes. For important data,
add literal `prevent_destroy = true` to protected database/table resources and
use a reviewed migration or transfer schema ownership to a modeling tool.
Never let Terraform and dbt/another tool manage the same table definition.
Review raw retention and compute sizes for your workload; this is not a sizing
recommendation. The examples use native storage, not DuckLake.

Token outputs are sensitive but still stored in bootstrap state. Demo tokens
expire in 30 days; rotation and updating deployment/BI secret stores are operational
responsibilities. Do not share bootstrap state simply to pass a username: use
non-secret outputs or explicit pipeline inputs.

## Cleanup and verification

For disposable examples, destroy warehouse roots using their original writer
tokens first, then destroy bootstrap last using the admin credential.
Deleting accounts/tokens first can strand databases and prevent cleanup.
Do not sweep unrelated databases or shares.

CI validates and plans all roots without credentials and executes the checked-in
SQL models in embedded DuckDB. This proves plan structure and model results,
not live cross-account authorization. Before production, verify as the BI account:
the marts share can be attached and queried, writes fail, and raw/transform and
the other environment are inaccessible unless explicitly shared.

See each root's README for sample data, expected results, and inputs.
