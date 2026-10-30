---
page_title: "motherduck_share_secure_view Resource - motherduck"
subcategory: "Access control"
description: |-
  Filters rows, hides columns, and masks values for the consumers of a MotherDuck share.
---

# motherduck_share_secure_view (Resource)

Manages one secure view on a MotherDuck share. Consumers of the share query the
secure view instead of the source table, so it can filter rows, leave columns
out, or replace values. The body can check the consumer with `current_user`,
`current_email()`, `session_roles()`, and `is_role_in_session()`. MotherDuck
enforces it on its servers. It does not restrict the owner of the source
database.

This resource requires provider v0.3.9 or later. A lock file that selects an
earlier 0.3.x release reports an unsupported resource type, so run
`terraform init -upgrade` after adding it.

Secure views need a Business or Enterprise plan, or an active free trial. They
work on native MotherDuck databases and MotherDuck-hosted DuckLake databases.
Run this resource with the share owner's read-write token. Only the share's
creator can change its secure views, and read-scaling tokens cannot alter
shares. See [secure shared rows and columns](../guides/row-and-column-security.md)
for policy design and [secure views](https://motherduck.com/docs/key-tasks/sharing-data/secure-views/)
for what the body may contain.

## Lifecycle

- With `replaces = true`, the secure view stands in for the table or view of the
  same name, which must already exist. Otherwise it adds a derived relation whose
  name must not exist yet. Changing `replaces`, `share`, `schema`, or `name`
  replaces the secure view. Terraform drops the old one before adding the new
  one, so for a moment consumers see the source table when the share's
  `include_pattern` matches it. With `include_pattern = []` they see no table
  instead.
- Changing `query` updates the secure view in place with `ADD OR REPLACE`.
  Consumers switch to the new policy within a minute or two and never see the
  source table without a policy in between.
- Create uses plain `ADD`. A secure view that already exists on the share fails
  the apply with its import ID instead of being overwritten.
- MotherDuck trims the body, so surrounding whitespace in `query`, such as the
  trailing newline of a heredoc, does not show as drift on refresh. Any other
  change made outside Terraform plans an update. A secure view dropped outside
  Terraform plans a create.
- When the secure view could be written but not read back, the apply succeeds
  with a warning and the next refresh reads it. The resource is not tainted, so
  no later apply drops and re-adds it.
- Replacing or dropping the share drops its secure views. Add
  `replace_triggered_by = [motherduck_share.<name>.created_ts]` so the same
  apply recreates them, as in the example.
- Destroying a replacing secure view makes its table visible to consumers again
  when the share's `include_pattern` matches the table. With
  `include_pattern = []`, the table disappears for consumers instead.
- A role grant or revoke takes up to about a minute to reach
  `is_role_in_session()`, in open sessions as well as new ones. A column masked
  by role membership can stay visible to a revoked role for that long.
- Terraform orders resources only by references. Add `depends_on` from
  `motherduck_share_grant` to the secure views so a new consumer is never granted
  a share whose policies do not exist yet.

## Example Usage

```terraform
resource "motherduck_database" "usage" {
  name = "usage"
}

resource "motherduck_schema" "app" {
  database = motherduck_database.usage.name
  name     = "app"
}

resource "motherduck_schema" "policy" {
  database = motherduck_database.usage.name
  name     = "policy"
}

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

# An empty include pattern exposes only the secure views. A missing policy
# hides its table instead of exposing it.
resource "motherduck_share" "usage" {
  name            = "usage_share"
  source_database = motherduck_database.usage.name
  access          = "restricted"
  visibility      = "hidden"
  update_mode     = "automatic"
  include_pattern = []
  depends_on      = [motherduck_table.daily_usage, motherduck_table.tenant_readers]
}

# Role grants and revokes take up to about a minute to reach
# is_role_in_session(), so contact_email masking follows role changes with
# that delay.
resource "motherduck_share_secure_view" "daily_usage" {
  share    = motherduck_share.usage.name
  schema   = motherduck_table.daily_usage.schema
  name     = motherduck_table.daily_usage.name
  replaces = true
  query    = <<-SQL
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
  SQL

  # Replacing the share drops its secure views. Recreate them in the same apply.
  lifecycle {
    replace_triggered_by = [motherduck_share.usage.created_ts]
  }
}

resource "motherduck_share_grant" "reader" {
  share    = motherduck_share.usage.name
  username = var.reader_username

  # Grant access only after the policy exists.
  depends_on = [motherduck_share_secure_view.daily_usage]
}

variable "reader_username" {
  description = "Existing grantable MotherDuck user or service-account principal."
  type        = string
  default     = "svc_reader_acme"
  nullable    = false
}
```

## Import

Import with `<share>.<schema>.<name>`:

```shell
terraform import motherduck_share_secure_view.daily_usage usage_share.app.daily_usage
```

The imported `query` is the body MotherDuck stores, without surrounding
whitespace. When the configuration spells it with surrounding whitespace, such
as a heredoc's trailing newline, the first plan after import shows an in-place
update. Applying it rewrites the same body with `ADD OR REPLACE`, which does not
change what consumers see. To list every secure view on a share, including ones Terraform does
not manage, read [motherduck_share_secure_views](../data-sources/share_secure_views.md).

<!-- schema generated by tfplugindocs -->
## Schema

### Required

- `name` (String) Name that consumers query. With `replaces = true` it must name an existing table or view in the source database. Otherwise it must not exist there. Changing only the case of ASCII letters updates state in place.
- `query` (String) Single SELECT body of the secure view, without surrounding parentheses. It reads only the share's source database and can call `current_user`, `current_email()`, `session_roles()`, and `is_role_in_session()`. Semicolons are rejected, including inside string literals. Use `chr(59)` for a literal semicolon. Leading and trailing whitespace is ignored when comparing with MotherDuck. Changes update the secure view in place with `ADD OR REPLACE`, so consumers never see the unfiltered table during the update.
- `share` (String) Name of the share that carries the secure view. The share must be owned by the identity of the SQL token. MotherDuck compares share names case-insensitively, so changing only the letter case updates state in place.

### Optional

- `replaces` (Boolean) Whether the secure view stands in for the table or view of the same name, so consumers query the policy output under the original name. Defaults to `false`, which adds a derived relation instead. Changing it replaces the secure view.
- `schema` (String) Schema of the secure view in the share's source database. Defaults to `main`. It must exist in the source database. Changing only the case of ASCII letters updates state in place.

### Read-Only

- `columns` (List of String) Columns the secure view returns to consumers, as reported by MotherDuck.
- `id` (String) Secure view resource ID in `<share>.<schema>.<name>` form.
