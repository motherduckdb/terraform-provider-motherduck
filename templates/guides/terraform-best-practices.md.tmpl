# Terraform Best Practices

This guide describes how to use the MotherDuck provider in production Terraform code. It focuses on module shape, state safety, credential handling, and predictable lifecycle management.

## Provider Requirements

Every root module and reusable child module should declare the provider source and a version constraint. For reusable modules, prefer a minimum provider constraint and let the root module choose the exact provider version.

```hcl
terraform {
  required_version = ">= 1.5.0"

  required_providers {
    motherduck = {
      source  = "motherduckdb/motherduck"
      version = ">= 0.1.0"
    }
  }
}
```

Provider configuration belongs in the root module. Child modules should receive provider configurations from the caller instead of defining their own tokens.

```hcl
provider "motherduck" {
  api_base_url = "https://api.motherduck.com"
}

module "tenant_data" {
  source = "./modules/tenant-data"

  providers = {
    motherduck = motherduck
  }
}
```

Use provider-level `database` only for an existing database that should be attached when the provider starts. Terraform configures providers before it creates resources, so `database = motherduck_database.example.name` cannot attach a database created in the same apply. For databases created by Terraform, set the `database` attribute on each SQL resource instead.

Use `attach_mode = "single"` only when the provider should attach the configured existing `database` without attaching other workspace databases during SQL initialization. DuckDB and MotherDuck system catalogs such as `memory` and `md_information_schema` can still appear in attached-database metadata. Leave `attach_mode` unset for MotherDuck's default workspace attachment behavior, or use `attach_mode = "workspace"` when making that behavior explicit helps reviewers. Workspace attachment is convenient for broad account administration, but it can expose a large attached-database catalog on real accounts; avoid publishing full attached-catalog outputs from reusable modules. Single attach mode is useful for BI, IDE, and app-style modules that should not see every workspace database, but it is a poor fit for bootstrap modules that create databases in the same Terraform run.

## Credentials

Use environment variables in automation instead of committing tokens into `.tf` or `.tfvars` files.

- `MOTHERDUCK_TOKEN` is used for SQL resources and data sources.
- `MOTHERDUCK_ADMIN_TOKEN` is used for REST administration resources and data sources.

Use `admin_token` only for organization administration workflows such as service accounts, access tokens, Duckling configuration, and active account inspection. Use the ordinary SQL token for database, schema, table, share, secret, snapshot, Dive, and Flight resources.

Leave `api_base_url` unset unless you are testing against a local server or routing through a controlled proxy. When you set it, use an absolute `http` or `https` URL with a host. Do not put query strings, fragments, credentials, or tokens in `api_base_url`.

The provider validates `api_base_url` and `attach_mode` during `terraform validate` so common configuration mistakes fail before credentials are needed.

Access token resources expose the newly created token once. Treat the resulting Terraform state as sensitive infrastructure data. Store state only in an encrypted backend with tightly scoped access.

## State Boundaries

Keep durable desired state in Terraform and keep operational actions out unless their state behavior is explicit.

Good Terraform fits:

- databases, schemas, tables, views, shares, share grants, secrets, snapshots, Dives, service accounts, access tokens, and Duckling configuration;
- read-only inspection with data sources;
- Flight definitions that should be managed as durable desired state.

Poor Terraform fits:

- one-off query execution;
- short-lived credentials that should not be persisted as durable resources;
- manual tenant data loads, backfills, and incident operations.

## Module Design

Prefer small modules with a clear ownership boundary:

- `tenant-database`: creates one tenant database, schema, and optional bootstrap tables.
- `tenant-share`: creates one share and one or more share grants.
- `service-account`: creates a service account and a token with a specific purpose.
- `duckling-config`: manages read-write and read-scaling sizing for one user.

Avoid modules that create both platform-level service accounts and all tenant databases unless the same team owns both lifecycles. Separate modules make state access, blast radius, and import behavior clearer.

For common multi-tenant starting points, see the included blueprint modules:

- [Writer bootstrap](../blueprints/writer-bootstrap.md): the dedicated writer service account and read-write token that own tenant data infrastructure.
- [Hypertenancy](../blueprints/hypertenancy.md): one isolated database, schema, restricted share, reader service account, and reader token per tenant.
- [Read hypertenancy](../blueprints/read-hypertenancy.md): centralized writes through one writer identity with per-tenant reader databases and shares.

MotherDuck databases are writable only by the identity that owns them, and `GRANT READ ON SHARE` can be run only by the share owner. Apply tenant data-plane modules with the writer service account's token so the writer owns the databases it must write and the shares it must grant.

## Naming

Use deterministic names so imports and drift investigations are simple.

```hcl
locals {
  tenant_slug   = replace(lower(var.tenant_id), "/[^a-z0-9_]/", "_")
  database_name = "tenant_${local.tenant_slug}"
  share_name    = "share_${local.tenant_slug}"
  reader_name   = "svc_reader_${local.tenant_slug}"
}
```

Keep generated names short enough for surrounding tools and logs. When a tenant identifier contains PII, use an internal tenant ID rather than an email or company name.

SQL object names may contain spaces, and table column names may contain spaces or embedded double quotes; the provider quotes those identifiers when issuing MotherDuck SQL. Prefer lowercase snake_case names for reusable modules anyway. They are easier to read in plans, pass through shell commands, and import later. Literal dots are not allowed in SQL resource names because Terraform import IDs use dots to separate database, schema, and object parts.

## Catalog Data Sources

Account-wide catalog data sources can return many rows in organizations with many databases or incoming shares. Scope them when possible before passing `rows_json` through reusable modules, outputs, or remote state. `motherduck_databases`, `motherduck_owned_shares`, `motherduck_shared_with_me`, and `motherduck_secrets` support a `name` filter for that reason. Use `motherduck_owned_share` when you need one owned share by name with typed attributes. Use broad catalog reads for audits and diagnostics, not as routine module inputs.

Treat `rows_json` as infrastructure metadata. It can include object names, owner metadata, share URLs, and tenant or environment naming conventions even when it does not include secret bodies. The provider marks row-style `rows_json` attributes sensitive so raw catalog rows are masked in plans and outputs by default. Use `nonsensitive(...)` only after reducing the value to a safe aggregate such as a count or boolean.

Row-style catalog data sources expose MotherDuck catalog values as returned by SQL. For example, `motherduck_owned_shares.rows_json` returns share options such as `ORGANIZATION`, `DISCOVERABLE`, and `MANUAL`, while the `motherduck_share` resource normalizes option attributes to lowercase Terraform values.

## Sensitive Outputs

Only output generated access tokens when the caller needs to pass them to a secret manager. Mark those outputs sensitive.

```hcl
output "reader_token" {
  value     = motherduck_access_token.reader.token
  sensitive = true
}
```

Do not print sensitive outputs with `terraform output -raw` in CI logs.

## SQL Function Availability

Dive and Flight SQL resources and data sources are enabled by default. Function availability can still vary by account, plan, region, and client version; the provider checks required SQL functions before resource operations and fails with an explicit availability diagnostic when a surface is not exposed.

## Lifecycle Behavior

Treat identity and create-only arguments as replacements. For example, changing a service account username, an access token name, token type, TTL, a database name or DuckLake path, a table column map, or a share definition should create a replacement resource instead of mutating the existing object. Service account usernames must start with an ASCII letter and contain only ASCII letters, digits, and underscores. Omit access-token `token_type` when you want the provider default of `read_write`.

SQL resource names must be non-empty, must not contain dots, and must not have leading or trailing whitespace. Spaces inside identifiers are supported for resources such as databases, schemas, tables, views, secrets, shares, and snapshots, but dots are reserved for Terraform import IDs such as `<database>.<schema>.<name>`.

Mutable configuration should update in place. `motherduck_duckling_config` updates instance sizes, read-scaling flock size, and cooldown settings for the same username. Public REST limits are validated locally: username fields must be non-blank and 1-255 characters; instance sizes accept `pulse`, `standard`, `jumbo`, `mega`, or `giga`; read-scaling flock size must be 0-64; cooldown values must be 60-86400 seconds; Dive/Flight SQL data-source IDs, Dive/Flight import IDs, and Dive embed-session IDs must be UUIDs with no leading or trailing whitespace. `motherduck_database.snapshot_retention_days`, `motherduck_view.query`, `motherduck_secret` parameters, and snapshot names also update in place.

Enum-like values must use lowercase canonical values. Use `database_type = "ducklake"`, `type = "s3"`, `access = "restricted"`, `token_type = "read_scaling"`, and Duckling instance sizes such as `standard`. Terraform validates this before apply so case-only differences do not turn into noisy post-apply plans.

Row-style data source pagination values must be nonnegative. Use `limit = 0` only when the underlying MotherDuck table function should return zero rows; otherwise set a positive limit or omit it. Flight log `run_number` values start at 1. Terraform validates these bounds before opening a MotherDuck SQL connection.

`motherduck_dive` metadata, content, and configured `required_resources` update in place when the public SQL table functions are available. Set `description = ""` to clear the visible description; removing an existing configured `description` is rejected because the current public Dive SQL surface does not expose a null-clear operation. The current public `MD_GET_DIVE` output does not expose mounted resources, so `required_resources` is config-owned during refresh and import.

`motherduck_flight` refreshes both the Flight summary and current FlightVersion. `config` and `flight_secret_names` replace the full stored map/list on update, so send the complete desired value instead of only the changed entry. Flight config keys become runtime environment variables, so they must be non-empty, must not use reserved MotherDuck runner parameter names, and must not contain `=` or NULL bytes. Omitting `access_token_name` uses MotherDuck's default Flight token behavior and remains unset in Terraform state. Removing `schedule_cron`, `requirements_txt`, `config`, or `flight_secret_names` from configuration clears the live value. Removing `access_token_name` is not supported as an in-place update by the current public Flight SQL surface; replace the Flight resource to return to default token behavior.

MotherDuck Pulse Duckling instances do not support cooldown seconds. Omit `read_write_cooldown_seconds` and `read_scaling_cooldown_seconds` when either corresponding instance size is `pulse`.

Snapshot resources manage snapshot names, not immediate physical deletion of retained snapshot bytes. Destroying a `motherduck_snapshot` removes the configured name and MotherDuck retention controls when unnamed snapshot data ages out. If a snapshot is unnamed outside Terraform, the next apply restores the configured named resource; MotherDuck may attach the name back to the same underlying `snapshot_id` while the unnamed snapshot remains retained.

Creating shares and snapshots depends on catalog metadata being visible immediately after SQL execution. The provider retries transient MotherDuck catalog errors such as timeouts or temporary unavailability and keeps Terraform state values known if a post-create read still fails. If an apply fails because MotherDuck cannot return share or snapshot metadata, rerun `terraform apply` after the transient clears so Terraform can refresh the created object before making further changes.

When managed SQL objects are deleted outside Terraform, the provider removes missing databases, schemas, tables, views, shares, secrets, and share grants from state during refresh so the next plan can recreate them. This is a repair mechanism for missing objects, not a substitute for reviewing every drift plan. If a database was dropped out of band, Terraform can recreate the object graph but cannot recover the deleted data. Secret metadata such as `scope` can be repaired when it is configured through `params`, but secret body values cannot be compared because MotherDuck does not return those values after creation.

Destroying `motherduck_database` drops the database and contained objects, including unmanaged tables or views. Use separate databases for data Terraform should never delete, and use `motherduck_schema.cascade_on_delete` only when schema-level cleanup should include unmanaged objects.

`motherduck_schema` destroys restrictively by default so Terraform does not silently remove unmanaged tables or views. Set `cascade_on_delete = true` only for schemas where Terraform is intentionally responsible for cleaning up everything left inside the schema at destroy time.

Table column changes replace `motherduck_table`. MotherDuck allows dropping the table even if an unmanaged view depends on it, so an unmanaged view can become invalid when the replacement removes referenced columns. Manage dependent views in Terraform, or keep table replacements compatible with unmanaged views that must continue working.

`motherduck_view` stores the server-rendered view definition in Terraform private state after create and update. User configuration stays authoritative in visible state, while out-of-band changes to the live `information_schema.views` definition appear as drift and are repaired by the next apply. View queries must be a single SELECT body and must not contain semicolons.

Share grants are reconciled from the share's public grantee metadata. If someone revokes a managed grant outside Terraform, the next plan should recreate it. Avoid managing the same share grants in both Terraform and ad hoc SQL unless you intentionally want Terraform to repair that drift. Share grants need a real grantable MotherDuck user or service-account principal; `motherduck_current_user` can return `duckdb`, and a PAT email or session name may not be accepted by `GRANT READ ON SHARE`. Share-grant usernames must be non-blank and free of leading or trailing whitespace, while still allowing email-like principals.

Share option values are validated locally. Use `access = "organization"`, `restricted`, or `unrestricted`; `visibility = "discoverable"` or `hidden`; and `update_mode = "manual"` or `automatic`. Omit options when you want MotherDuck defaults.

Treat share option drift as a replacement event. MotherDuck can recreate a share with different access, visibility, or update mode outside Terraform, but Terraform models those options as immutable desired state. The next plan should replace the share to restore configuration, which creates a new share URL and may require consumers to reattach.

Share URLs are marked sensitive by the provider because unrestricted share URLs are access-bearing metadata. Terraform still stores sensitive values in state. When you need to hand a share URL to a consumer, expose it through a sensitive output or a secret manager instead of printing plans or CI logs.

Secret options are also validated before SQL execution. Use bare SQL option words for `type`, `secret_provider`, and `params` keys. Prefer `params` for ordinary key/value secret clauses because values are quoted safely and known metadata such as `scope` can be drift-checked. Reserve `secret_sql` for advanced raw clauses that are not yet modeled as first-class attributes, and never include semicolons.

## Imports

Import durable objects before letting Terraform manage them:

```bash
terraform import motherduck_database.analytics analytics
terraform import motherduck_schema.core analytics.core
terraform import motherduck_table.events analytics.core.events
terraform import motherduck_view.daily analytics.core.daily
terraform import motherduck_secret.s3_loader tenant_s3_loader
terraform import motherduck_share.analytics analytics_share
terraform import motherduck_share_grant.customer analytics_share/svc_reader_tenant
terraform import motherduck_snapshot.monthly analytics.monthly_snapshot
```

Access tokens import with `<username>/<token_id>`:

```bash
terraform import motherduck_access_token.reader svc_reader_tenant/c04c2f00-10ad-4ed7-acb7-f2b993b536b3
```

After import, run `terraform plan` and align configuration to the imported state before applying changes. Imported views read the canonical SELECT body from `information_schema.views.view_definition`; put that SELECT body in `query`, not the surrounding `CREATE VIEW ... AS` DDL. Imported databases can recover readable fields such as `database_type` and `snapshot_retention_days`, but DuckLake `encrypted` and `data_path` are create-only and are not exposed by the public catalog; omit those fields after import unless you intentionally want Terraform to replace the database. Imported secrets can recover public metadata such as `type`, `secret_provider`, `storage`, and `scope`, but secret parameters are write-only and must be supplied intentionally if you want Terraform to rotate or replace the secret body. Imported access tokens can recover metadata such as name, type, timestamps, and read-only status, but the token secret is only returned at creation time.

SQL resource names must be non-empty, cannot contain literal dots, and cannot have leading or trailing whitespace. Terraform import IDs use dots as separators, such as `database.schema.table`, so the provider rejects dotted SQL resource names during validation instead of creating resources that cannot be imported or addressed unambiguously.

Import IDs may contain spaces when the underlying MotherDuck object names contain spaces. Quote the entire import ID as one shell argument, for example `terraform import motherduck_table.events "tenant db.app schema.fact table"`. Import ID segments must be non-empty; malformed IDs such as `db..table`, `share/`, or `/token_id` are rejected before any live read. Service-account imports use the same username rules as configuration, and Duckling/access-token username import segments must be non-blank, at most 255 characters, and free of leading or trailing whitespace. Share-grant username import segments must be non-blank and free of leading or trailing whitespace, while still allowing email-like principals. Access-token ID import segments must also omit leading and trailing whitespace. For reusable modules, prefer simple names so imports, shell commands, and operational runbooks remain copy-pasteable.

For database kind, use `database_type = "ducklake"` only when you are intentionally creating a DuckLake-backed database. Omit `database_type` or set `database_type = "default"` for ordinary databases. Use `transient = true` for transient databases; `database_type = "transient"` is rejected during validation. `data_path` and `encrypted` are DuckLake-only settings. Use `encrypted = true` for encrypted DuckLake storage; the provider emits the parser-supported bare `ENCRYPTED` option.

`snapshot_retention_days` must be nonnegative. The provider validates negative values locally and lets MotherDuck enforce any account-specific upper bound.

Blueprint modules intentionally constrain generated names more tightly than raw SQL resources. Use ASCII letter-starting alphanumeric/underscore prefixes and service-account names, and let the module normalize tenant keys or slugs into lowercase alphanumeric/underscore suffixes. This keeps generated database, share, service-account, and token names easy to import, quote in shells, and reuse across Terraform versions.

For table columns, DuckDB type aliases are accepted. The provider compares configured and refreshed column types semantically by round-tripping both type strings through DuckDB, so aliases such as `INT` and canonical names such as `INTEGER` do not create replacement plans by themselves. Semantic column changes still replace `motherduck_table`.

## Validation Workflow

Use ordinary Terraform validation in root modules and reusable modules:

```bash
terraform fmt -check -recursive
terraform init
terraform validate
terraform plan -out=tfplan
terraform apply tfplan
```

For reusable modules, also validate a small fixture that exercises default inputs and at least one realistic tenant or database configuration. Keep module outputs narrow and mark generated tokens, share URLs, and row-style catalog JSON outputs as sensitive.

Provider contributors should use the repository targets described in [Contributing](../../CONTRIBUTING.md) instead of copying provider-development commands into customer modules.

```bash
make pre-push-check
make docs
make test-integration
make test-acceptance
make test-terraform-versions
MOTHERDUCK_TOKEN=... make test-terraform-versions-lifecycle
```
