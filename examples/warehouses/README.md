# Warehouse examples

Three independent Terraform roots demonstrate environment ownership and two warehouse sizes.

| Root | Purpose | Credential |
| --- | --- | --- |
| [bootstrap](bootstrap/README.md) | Create dev/prod writers, ingestion/BI tokens, and read fleets | MOTHERDUCK_ADMIN_TOKEN |
| [simple](simple/README.md) | One database, raw orders, daily revenue view | Environment writer's MOTHERDUCK_TOKEN |
| [layered](layered/README.md) | Raw/transform/marts databases | Environment writer's MOTHERDUCK_TOKEN |

These follow MotherDuck's [resource-management](https://motherduck.com/docs/concepts/resource-management)
and [environment-management](https://motherduck.com/docs/key-tasks/data-warehousing/environment-management)
guidance. Layout follows
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
  ├── dev_writer  ── owns dev warehouse  ── dev_bi read-scaling token
  └── prod_writer ── owns prod warehouse ── prod_bi read-scaling token
```

Both writers own all layers of their respective environment in these examples.
BI uses a separate read-scaling token on the same writer identity. It can read
all data visible to that writer, including raw and transform. The `dev_bi` and
`prod_bi` output keys identify tokens, not separate accounts.
Service accounts are an isolation boundary within an organization, not separate
organizations or protection against privileged administrators.

## Deployment order

1. Run `terraform init` to install the provider from the [Terraform Registry](https://registry.terraform.io/providers/motherduckdb/motherduck/latest). For an offline mirror, see [direct installation](../../docs/guides/github-installation.md).
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
   intended bootstrap writer username. This first read-write connection also
   initializes the account before any BI read-scaling connection.
6. Apply dev first, validate data and access, then apply the same reviewed code
   to prod with the prod token, state, and variables. Promote code, not dev state.
   Do not copy production data into dev automatically.
7. Configure the BI tool with `dev_bi` or `prod_bi` from the matching writer
   account, then query that environment's database directly. No cross-account
   share or initial share attachment is needed for this chosen approach.

Do not configure a provider using a token resource created in the same apply.
Provider configuration precedes resource creation. The two-phase bootstrap
boundary is intentional.

## BI access decision

The default uses the writer account's **read-scaling token** for BI. It keeps both
warehouse layouts runnable without a second identity or a share-attachment step.
Bootstrap sets `read_scaling_flock_size` to dev = 1 and prod = 2, with Standard
instances and 60-second idle cooldowns. Override the per-environment fleet limits
for expected BI concurrency and cost. These are small example defaults, not
measured production sizing. Confirm read-scaling availability for your account.
[Read replicas are eventually consistent](https://motherduck.com/docs/key-tasks/authenticating-and-connecting-to-motherduck/read-scaling/),
so allow for synchronization after ingestion or marts refresh.

### Alternative: separate readers with curated shares

Choose separate environment-specific reader service accounts when BI must only
see curated data. For each environment:

1. Create its reader account and tokens in the admin bootstrap. Keep a
   `read_write` token for initial setup and a `read_scaling` token for BI.
   Omit `ttl` here too to follow this example's non-expiring-token choice.
   Configure `motherduck_duckling_config` on the **reader**, including its fleet
   size when using read scaling.
2. Using the writer, create a `motherduck_share` for the curated database with
   `access = "restricted"`, `visibility = "hidden"`, and
   `update_mode = "automatic"`. Add `motherduck_share_grant` with
   `grantee_type = "user"` and that environment's reader username.
   In the layered layout, share only the physical marts database. In the simple
   layout, sharing the whole database also exposes raw data; materialize a
   separate curated database if that is unsuitable.
3. **Connect as the reader with its read-write token and attach the share at
   least once**, before starting BI. Obtain the exact share URL from the writer's
   sensitive share output and execute:

   ```sql
   ATTACH '<marts_share_url>' AS reporting;
   SELECT * FROM reporting.main.daily_revenue;
   ```

   This initializes the reader and persists its attachment. A share grant alone
   does not attach it, and a read-scaling token cannot perform `ATTACH`.
4. Give BI only the reader's read-scaling token and query
   `reporting.main.daily_revenue`. Verify reads succeed, writes fail, and raw,
   transform, and the other environment remain inaccessible without grants.

Repeat the grant and initial attachment for the respective dev and prod readers.
The two approaches offer different data access: read scaling on the writer does
not restrict BI to marts. See [read-scaling permissions](https://motherduck.com/docs/key-tasks/authenticating-and-connecting-to-motherduck/read-scaling/#permissions).

## Lifecycle and operation boundaries

Terraform owns database, schema, table, and view definitions. Ingestion
and data refresh are separate SQL/pipeline operations; these examples create no
Flight runs, Dives, or Guides. A fresh apply creates an empty warehouse.

The table resource uses replacement for schema changes. For important data,
add literal `prevent_destroy = true` to protected database/table resources and
use a reviewed migration or transfer schema ownership to a modeling tool.
Never let Terraform and dbt/another tool manage the same table definition.
Review raw retention and compute sizes for your workload; this is not a sizing
recommendation. The examples use native storage, not DuckLake.

Token outputs are sensitive but still stored in bootstrap state. Example tokens
never expire: `ttl` is deliberately omitted to keep setup simple. They remain
valid until revoked, as described by the [token API](https://motherduck.com/docs/sql-reference/rest-api/users-create-token/).
Revoke them when no longer needed and protect the bootstrap state. Do not share
bootstrap state simply to pass a username: use non-secret outputs or explicit
pipeline inputs.

## Cleanup and verification

For disposable examples, destroy warehouse roots using their original writer
tokens first, then destroy bootstrap last using the admin credential.
Deleting a service account permanently deletes the account and all data it owns.
Deleting tokens first can strand databases by removing the credentials needed to
clean them up. Always destroy warehouse data before bootstrap accounts.
Do not sweep unrelated databases or shares.

CI validates and plans all roots without credentials and executes the checked-in
SQL models in embedded DuckDB. This proves plan structure and model results,
not live authorization. Before production, verify using each BI token that
reads succeed, writes fail, and the other environment is inaccessible unless
explicitly shared. Raw/transform reads in the same environment are expected.

See each root's README for sample data, expected results, and inputs.

## Updating an already-applied version

The earlier bootstrap created separate `dev_bi`/`prod_bi` accounts. This version
removes those accounts, replaces their tokens with writer-owned tokens, and
removes the layered share/grant. Token names and expiry settings also change.
Review the replacement plan, detach old shares as the old reader with a
read-write token, remove the old layered grants/shares with the writer, then
apply bootstrap and securely update both pipeline and BI credentials. Keep the
original writer credentials available until warehouse changes finish. Do not
apply this switch if existing BI consumers still require curated-only access;
retain the separate-reader design above instead.
