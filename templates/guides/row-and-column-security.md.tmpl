---
page_title: "Secure shared rows and columns"
subcategory: "Operations"
description: |-
  Filter rows, hide columns, and mask values on shares, and decide which workflow owns each access policy.
---

# Secure shared rows and columns

MotherDuck enforces row-level security, column-level security, and column
masking through secure views attached to a share. A secure view is a `SELECT`
that consumers of the share receive in place of a table. It can filter rows,
omit columns, or replace values, and it can check the identity of the consumer
running the query. MotherDuck evaluates it on its servers, so a modified client
cannot bypass it.

Secure views require a Business or Enterprise plan, or an active free trial.
They work on native MotherDuck databases and MotherDuck-hosted DuckLake
databases. DuckLake databases with their own `DATA_PATH` and Iceberg databases
are not supported. See
[secure views](https://motherduck.com/docs/key-tasks/sharing-data/secure-views/)
for the complete SQL reference.

## How the controls fit together

| Control | Mechanism | Same for every consumer | Managed by this provider |
| --- | --- | --- | --- |
| Platform permissions | Preset roles `admin`, `builder`, and `explorer`, inherited by custom roles | Per role | `motherduck_role`, `motherduck_role_grant` |
| Who can read a share | Share grants and the share `access` mode | Per grantee | `motherduck_share`, `motherduck_share_grant` |
| Which tables a share exposes | `include_pattern`, also called table-level security | Yes | `motherduck_share.include_pattern` |
| Which rows and columns each consumer sees | Secure views | No, they can check the consumer's identity | No resource yet. Apply them with SQL as the share owner |

Every data control lives on the share. MotherDuck has no `GRANT SELECT ON
TABLE` and no policy object attached to a table. Secure views also do not
restrict the owner of the source database. The writer identity, and anyone
holding its tokens, sees every row. Never answer consumer requests with writer
credentials.

A `motherduck_view` is an ordinary DuckDB view. It does not restrict access and
cannot stand in for a secure view.

## Decide where each policy lives

Give each object one owner:

| Object | Owner | Why |
| --- | --- | --- |
| Reader service accounts, tokens, custom roles, and role membership for service accounts | Terraform admin root | Identities change rarely and need review |
| Role membership for people | The identity provider, when it is the source of truth | MotherDuck refuses `motherduck_role_grant` for people in that case |
| Share, `include_pattern`, and grants | Terraform root that runs as the share owner | This is the publication boundary, so plans should show changes to it |
| Secure view SQL | The same repository and identity as the share, applied by a policy step after `terraform apply` | Only the share creator can change its secure views, and replacing the share drops them |
| Tables and schemas | Terraform or the data pipeline, as described in [resource scope](resource-scope.md) | Structure |
| Entitlement rows, such as which reader belongs to which tenant | Onboarding automation or the pipeline | These rows change with customers |
| Per-request authentication | The application backend | Terraform does not issue sessions |

Review a secure view change with the share and grant changes it depends on. A
row filter is part of the publication contract, and reviewers need both halves
to judge who can read what.

## Design policies to fail closed

Tables that match `include_pattern` and have no secure view are fully visible
to every consumer. A secure view stays visible even when the pattern excludes
its source table. Combine the two so that a missing policy hides a table
instead of exposing it:

```hcl
resource "motherduck_share" "usage" {
  name            = "usage_share"
  source_database = motherduck_database.usage.name
  access          = "restricted"
  visibility      = "hidden"
  update_mode     = "automatic"
  include_pattern = []

  lifecycle {
    prevent_destroy = true
  }
}
```

With `include_pattern = []`, consumers see the secure views and nothing else.
If a secure view is dropped, its table disappears for consumers. Add tables
that need no policy to the pattern by name. Macros, sequences, and types in the
default `main` schema remain visible through every filtered share, so keep
sensitive objects out of `main`.

Follow the same rule inside each policy:

- Filter by joining an entitlement table. A consumer without a matching row
  receives no rows.
- List columns explicitly instead of `SELECT *`. A new column then stays hidden
  until the policy includes it.
- Keep entitlement tables and policy macros in a schema the pattern excludes.
  Secure views can still read them, and consumers cannot. Dropping or breaking
  a macro makes the secure views that call it fail.
- Match people with `current_email()` because usernames are unique only within
  an organization. Service accounts have no email, so match them with
  `current_user`.
- Use `is_role_in_session('role_name')` to gate a column on role membership.
  MotherDuck has no `current_role` function.

## Example: one table for many tenants

Every tenant's rows live in `app.daily_usage`. Each tenant has its own reader
service account, as in the [customer-facing analytics guide](customer-facing-analytics.md).
One share serves all of them, and a secure view returns each reader only its
tenant's rows.

Terraform creates the structure, one share, and one grant per reader:

```hcl
resource "motherduck_table" "daily_usage" {
  database = motherduck_database.usage.name
  schema   = motherduck_schema.app.name
  name     = "daily_usage"
  columns = {
    tenant_id     = "VARCHAR"
    usage_date    = "DATE"
    event_count   = "BIGINT"
    contact_email = "VARCHAR"
  }
}

resource "motherduck_table" "tenant_readers" {
  database = motherduck_database.usage.name
  schema   = motherduck_schema.policy.name
  name     = "tenant_readers"
  columns = {
    reader_name = "VARCHAR"
    tenant_id   = "VARCHAR"
  }
}

resource "motherduck_share_grant" "reader" {
  for_each     = var.tenants
  share        = motherduck_share.usage.name
  username     = motherduck_service_account.reader[each.key].username
  grantee_type = "user"
}
```

This is an excerpt. The share is the fail-closed share above. Readers and
schemas follow the customer-facing analytics example.

The policy step replaces `app.daily_usage` for consumers. It filters by tenant
and returns `contact_email` only to readers in the `usage_pii_readers` role:

```sql
ALTER SHARE usage_share
  ADD OR REPLACE SECURE VIEW REPLACES app.daily_usage AS (
    SELECT
      u.tenant_id,
      u.usage_date,
      u.event_count,
      CASE WHEN is_role_in_session('usage_pii_readers')
        THEN u.contact_email
      END AS contact_email
    FROM app.daily_usage AS u
    JOIN policy.tenant_readers AS r ON r.tenant_id = u.tenant_id
    WHERE r.reader_name = current_user
  );
```

Onboarding automation writes the entitlement as the writer:

```sql
INSERT INTO usage.policy.tenant_readers (reader_name, tenant_id)
VALUES ('svc_reader_acme', 'acme');
```

Both halves are required. A grant without an entitlement row returns no rows,
and an entitlement row without a grant cannot be attached. To suspend a tenant,
revoke its grant with Terraform. To offboard it, also delete its entitlement
row and its data rows.

Each reader still attaches the share once with a read-write setup token and
queries through its own Duckling and read pool. Pooling the data does not pool
reader compute. See [choose a tenancy model](customer-facing-analytics.md#choose-a-tenancy-model)
before moving tenants out of dedicated databases.

## Apply policies after Terraform

Terraform does not manage secure views yet. Keep their SQL in the repository
with the share and run a policy step in the same pipeline, right after
`terraform apply`:

1. Connect with the share owner's read-write token. Read-scaling tokens cannot
   alter shares, and only the share's creator can change its secure views.
   Managing secure views with SQL needs DuckDB 1.5.6 or later.
2. Run one `ALTER SHARE` statement that uses `ADD OR REPLACE SECURE VIEW` for
   every policy. Actions in one statement apply all or nothing, and the step can
   run again without changing the result. Drop removed policies with `DROP
   SECURE VIEW IF EXISTS` in the same statement.
3. Run `SHOW SECURE VIEWS ON SHARE usage_share` and compare the `sql` column
   with the repository. Fail the pipeline when they differ.

Run the step after every apply, including applies that did not touch the policy
files. Terraform state has no record of secure views, so a no-op plan does not
show that they exist. Replacing a share drops its secure views and changes its
URL. Changing `name`, `source_database`, `access`, `visibility`, or
`update_mode` replaces a share, which is why the example sets `prevent_destroy`.
Do not run `CREATE OR REPLACE SHARE` on a share with secure views. It drops every
secure view the statement does not restate.

Secure view changes reach attached consumers within a minute or two, even on
`manual` shares. Changes to entitlement rows and macros follow the share's
publication cycle. On a `manual` share, run `UPDATE SHARE` after changing them.

## Verify as each consumer

Identity functions evaluate as the session that runs the query, so test with
each consumer's own credentials:

| Check | Expected result |
| --- | --- |
| Acme's reader queries `reporting.app.daily_usage` | Only rows where `tenant_id = 'acme'` |
| Globex's reader queries the same relation | Only Globex rows |
| A granted reader without an entitlement row queries it | No rows |
| A reader outside `usage_pii_readers` selects `contact_email` | `NULL` in every row |
| A consumer queries `policy.tenant_readers` | The table does not exist |
| The writer queries the source database | Every row. This is why the writer never serves consumers |

## Consumer constraints

- Consumers need DuckDB 1.5.5 or later for every query shape. Versions 1.4.0 to
  1.5.4 fail on `ORDER BY ... LIMIT`, `LIMIT ... OFFSET`, and `USING SAMPLE`.
  Check the DuckDB version your application's client library embeds.
- Consumers cannot read `rowid` through a secure view.
- `CREATE DATABASE ... FROM` a share with secure views is refused. Use `COPY
  FROM DATABASE` instead. Copies contain only the rows and columns the consumer
  can see.
- DuckLake shares with secure views must use `update_mode = "automatic"`.
  Consumers of those shares cannot time travel or call DuckLake metadata
  functions.
- A share holds at most 100 secure views. Each view's SQL is limited to 256 KiB
  and the total to 4 MiB per share.
- A secure view body reads only the shared database. It cannot use table
  functions, `LIMIT`, `SAMPLE`, volatile functions such as `random()`, or
  session functions such as `current_setting()`.

See [manage roles and share access](access-control.md) for role membership and
grant audits.
