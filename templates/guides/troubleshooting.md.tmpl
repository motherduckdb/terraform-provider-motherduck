---
page_title: "Troubleshooting"
subcategory: "Operations"
description: |-
  Common provider diagnostics, what causes them, and how to resolve each one.
---

# Troubleshooting

Find the diagnostic summary or message text in the tables below. Each row gives
the cause and the change that resolves it. The text in the first column is
quoted from the provider so that it can be searched.

## Credentials and connectivity

| Diagnostic | Cause | Resolution |
| --- | --- | --- |
| `MotherDuck token required`, or the plan warning `MotherDuck token not configured` | A SQL resource or data source ran without a SQL token. | Set the provider `token` argument or `MOTHERDUCK_TOKEN`. An empty argument falls back to the environment variable. |
| `MotherDuck admin token required`, or the plan warning `MotherDuck admin token not configured` | A service account, access token, or Duckling configuration ran without an organization admin token. | Set the provider `admin_token` argument or `MOTHERDUCK_ADMIN_TOKEN`. The SQL token does not authorize REST administration. |
| `MotherDuck rejected the SQL token` | The token is expired, revoked, or copied incorrectly. | Create a new token for the same account and supply it through the secret manager. |
| `Could not connect to MotherDuck` with the note that it is not a token problem | The machine cannot reach MotherDuck because of a network, proxy, DNS, or TLS failure. | Read the gRPC message at the end of the error. It names the step that failed. Replacing the token does not help. |
| `MotherDuck SQL statement did not finish within 30m0s and was canceled` | A SQL statement got no answer from MotherDuck. The provider stops a statement after 30 minutes when the resource has no `timeouts` block. | Check connectivity and the MotherDuck status page, then run the operation again. For a long-running database or snapshot operation, raise the value in its `timeouts` block. |
| `lacks platform authorization` on a role or role grant | The SQL identity cannot manage roles. | Use a SQL token for a user or service account whose role allows role management. The admin token does not authorize SQL role management. |
| `Unknown MotherDuck token`, or another `Unknown MotherDuck ...` provider error | A provider argument depends on a value that is not known until apply. | Supply provider arguments from variables or the environment. Create identities in one Terraform root and use their tokens in another, as described in [authentication](authentication.md). |

## Ordering between resources

Terraform orders resources only through references. A literal name that happens
to match another resource does not create a dependency.

| Diagnostic | Cause | Resolution |
| --- | --- | --- |
| `Unable to attach MotherDuck database` for a database that the same configuration creates | The dependent resource names the database with a literal string, so Terraform did not create the database first. | Reference the resource, for example `database = motherduck_database.analytics.name`. |
| `Unable to create MotherDuck table` or `Unable to create MotherDuck view` with a note that the schema does not exist | The schema is created in the same configuration but is named with a literal string. | Reference the resource, for example `schema = motherduck_schema.marts.name`. |
| `MotherDuck share not found` on a share secure view | The share does not exist yet, or the SQL identity does not own it. | Reference `motherduck_share.<name>.name`, and run the configuration as the share owner. |
| `Unable to drop MotherDuck database` mentioning `database shares linked to it` | A share still points at the database. The provider does not drop shares with a database. | Set the share `source_database` to `motherduck_database.<name>.name` so that Terraform destroys the share first, or drop the share manually. |
| `Unable to drop MotherDuck schema` because the schema still contains objects | The schema holds objects that this configuration does not destroy first. | Remove those objects, or set `cascade_on_delete = true`, apply that change, and then destroy the schema. |

## Objects that already exist or no longer exist

| Diagnostic | Cause | Resolution |
| --- | --- | --- |
| A create fails with `already exists` and a suggested import ID | The object exists in MotherDuck but is not in Terraform state. | Import it with the ID from the message, in an `import` block or with `terraform import`, or choose a different name. See [state and imports](state-and-lifecycle.md). |
| Warning `MotherDuck access token not found` | The token was deleted or expired outside Terraform and was removed from state. | Apply to create a replacement token with a new secret, and update the systems that use it. When the token still exists, import it again instead of applying. |
| `MotherDuck user is not a service account` | The username in state belongs to a human user. The provider manages only service accounts because deleting a user deletes the data it owns. | Remove the resource from state with `terraform state rm` and manage the user outside Terraform. |
| `Unable to confirm MotherDuck service account` | The user exists but the organization listing skipped it while other users changed. | Run the operation again. Terraform kept the resource in state. |
| Warning `MotherDuck snapshot outlived its database` | The database was dropped while its named snapshot is still retained and billed. | Destroy the snapshot resource to release the snapshot name. |

## Plans that replace or refuse to change a resource

| Diagnostic | Cause | Resolution |
| --- | --- | --- |
| A table plan shows `must be replaced` | The type of an existing column, the primary key, or the order of existing columns changed. Replacing the table removes its rows. Adding or dropping a column does not replace the table. | Migrate the data with a reviewed SQL migration, or move schema ownership to the modeling tool. See the [table resource](../resources/table.md). |
| `Unable to update MotherDuck table` naming a `SET NOT NULL` statement | The column already holds a `NULL`, or a DuckLake table has no statistics for the column yet. | Backfill the column and apply again. Terraform recorded the statements that succeeded. |
| `Unable to compare MotherDuck table column types` | The provider could not resolve a column type during planning, so it refused to plan a replacement. | Check connectivity. Qualify a user-defined type with its database and schema, such as `analytics.main.mood`, or write out its definition. |
| `Service account description cannot change` | MotherDuck sets the description only when it creates the account. Replacing the account would delete its data. | Restore the previous description, or follow the steps in the message to record a new description without changing the account. |
| `Unsupported MotherDuck database name spelling` | The new name differs from the existing one only in the case of a non-ASCII letter, which MotherDuck cannot attach. | Keep the existing spelling. |
| `Table updates are not supported`, `Role updates are not supported`, or a similar update error | An argument that cannot change in place changed without a planned replacement. | Replace the resource, for example with `terraform apply -replace=<address>`, after confirming that losing the object is acceptable. |

## Features that depend on the account

| Diagnostic | Cause | Resolution |
| --- | --- | --- |
| `MotherDuck SQL function unavailable` or `MotherDuck SQL command unavailable` | The account, plan, or region does not offer the feature that the resource or data source needs. | Confirm the feature is enabled for the organization. See [resource scope](resource-scope.md) for preview and experimental surfaces. |
| `Unable to set MotherDuck Duckling config` with a note about plan limits | The requested instance size or flock size exceeds the limits of the organization plan. | Choose a smaller size, or ask MotherDuck to raise the limits. The message lists the default limits for each plan. |
| A role grant fails mentioning `role assignment source of truth` | The organization manages user roles through its identity provider. MotherDuck accepts role grants only for service accounts and roles. | Manage that user in the identity provider. |
| `MotherDuck did not store the access token description` | The MotherDuck region does not support token descriptions yet. | Remove `description` from the configuration, or retry after the region is updated. |

## Collect diagnostics

1. Run the failing command with `TF_LOG=DEBUG` and save the output to a file.
   The provider logs each SQL boot query with the token redacted and the method,
   path, status code, and duration of each REST request.
2. Reduce the configuration to the smallest one that reproduces the failure.
3. Remove tokens, share URLs, account names, and state contents before sharing
   the log. Debug logs from Terraform itself can include configuration values.
4. Open a [GitHub issue](https://github.com/motherduckdb/terraform-provider-motherduck/issues)
   with the provider version, the Terraform or OpenTofu version, the reduced
   configuration, and the redacted diagnostics.
