# Changelog

Each release has a versioned notes file under `release-notes/`, which is also the
body of the matching [GitHub release](https://github.com/motherduckdb/terraform-provider-motherduck/releases).
This index lists the headline change for every release.

## Unreleased

- Changing a share's `include_pattern` updates it in place. Omitted share options no longer force replacement.
- Respelling an imported table column type with an alias, such as `INT` for `INTEGER`, no longer replaces the table.
- Secrets with uppercase names are found after creation, and destroying a missing secret succeeds.
- Creating a view fails instead of overwriting an existing view with the same name.
- Guide creation is no longer retried, which prevented duplicate Guides after timeouts.
- Guide `references` treat an empty `schema`, `table`, or `column` string the same as an omitted value.
- Catalog row JSON renders DECIMAL values as numbers, INTERVAL values as DuckDB text, and nested UUID values as strings.
- Data source listings return rows in a documented, deterministic order. `files`, `buckets_for_secret`, and `flight_logs` accept `limit` and `offset`.
- Provider configuration rejects unknown `api_base_url`, `database`, `attach_mode`, and `custom_user_agent` values, and accepts plain HTTP `api_base_url` values only for loopback hosts.
- Duckling config cooldowns record the live value, so import and refresh no longer plan an update. An unset cooldown keeps its current value while the instance size stays the same, and removing a configured cooldown no longer resets it.
- Release packages run on Linux with glibc 2.34 or newer and on macOS 13 or newer.
- `motherduck_database` supports `database_type = "iceberg"` with an `iceberg` block that registers an existing Iceberg REST catalog, such as Amazon S3 Tables or AWS Glue.
- `motherduck_secret` supports `FLIGHTS` secrets through a sensitive `flight_params` map.
- `motherduck_access_token` accepts an optional `description`, and `motherduck_user_tokens` returns it. Token names `MotherDuck Extension` and `MotherDuck Flights` are rejected at plan time.
- Service account usernames must be at least 3 characters, matching the API.
- `motherduck_dive_embed_session` accepts `session_name`, `version`, `required_resources`, and `initial_state`. `session_hint` is deprecated in favor of `session_name`.
- `motherduck_flight` reports `schedule_status` and `owner_name`, rejects the reserved config keys `MOTHERDUCK_FLIGHT_ID` and `MOTHERDUCK_FLIGHT_RUN_ID`, and checks the 200 KB source and 20 KB requirements limits at plan time. The `max_runtime_sec` docs describe the per-plan caps.
- `motherduck_flight_run` reads runs with `MD_GET_FLIGHT_RUN` and falls back to the run listing.
- `motherduck_flight_logs` pages on the server and accepts `order`, so lines before the last 1,000 are reachable.
- Destroying a snapshot whose database was dropped now releases its retained name.
- `motherduck_dive` reads `api_version` and `required_resources` on refresh and import, so an import no longer needs a corrective update.
- Guide role access is detected by the `role_names` parameter of `MD_SET_GUIDE_ACCESS`, and `motherduck_guide_grantees` reads roles from `MD_GET_GUIDE`. Role access stays experimental and is not yet available in MotherDuck.
- Docs: filtered shares and custom roles need a Business plan or an active free trial, filtered shares reject DuckLake and Iceberg databases, Flight listings default to 50 rows, and `motherduck_role_members` reports a null `email` for service accounts.

## v0.2.13

Follow the current, version-pinned examples with safer token rotation and complete copyable Terraform roots. See the [v0.2.13 release notes](release-notes/v0.2.13.md).

## v0.2.12

Retain a newly created view's Terraform state when its immediate catalog read returns no row or an error. See the [v0.2.12 release notes](release-notes/v0.2.12.md).

## v0.2.11

Follow complete examples from provisioning through reader setup, token rotation, runtime workloads and teardown. A Registry guide links the version-pinned projects. See the [v0.2.11 release notes](release-notes/v0.2.11.md).

## v0.2.10

Audit share audiences against an explicit allowlist and optionally fail Terraform plans when missing or unexpected grants persist. See the [v0.2.10 release notes](release-notes/v0.2.10.md).

## v0.2.9

Preserve snapshot state after a successful rename when catalog readback fails, so retries and cleanup keep the correct identity. See the [v0.2.9 release notes](release-notes/v0.2.9.md).

## v0.2.8

Expand live regression coverage for database recovery, account and token changes, and writer/reader isolation while preserving provider schemas and features. See the [v0.2.8 release notes](release-notes/v0.2.8.md).

## v0.2.7

Respect canceled SQL requests before starting an operation, and reduce allocation overhead when converting mixed-value catalog rows. See the [v0.2.7 release notes](release-notes/v0.2.7.md).

## v0.2.6

Keep newly created databases, roles, and role grants manageable when the following catalog read is empty or fails. See the [v0.2.6 release notes](release-notes/v0.2.6.md).

## v0.2.5

Recover cleanly when a Dive or Guide is deleted outside Terraform, and get consistent diagnostics when a Flight run times out. See the [v0.2.5 release notes](release-notes/v0.2.5.md).

## v0.2.4

Follow clearer architecture diagrams for provisioning, layered warehouses, tenant isolation, and Blueprints promotion, with explicit account ownership and compute boundaries. See the [v0.2.4 release notes](release-notes/v0.2.4.md).

## v0.2.3

Build an orders warehouse, separate environments, or serve customer analytics with practical guides, runnable examples, and concise architecture diagrams. See the [v0.2.3 release notes](release-notes/v0.2.3.md).

## v0.2.2

Release checksums now carry a detached GPG signature, which is the last artifact the Terraform Registry requires to ingest a release. See the [v0.2.2 release notes](release-notes/v0.2.2.md).

## v0.2.1

Adds repeatable plan/apply checks for unintended replacement and identity drift across the checked-in examples. See the [v0.2.1 release notes](release-notes/v0.2.1.md).

## v0.2.0

In-place database and Flight updates preserve dependent infrastructure and avoid unintended Flight re-execution. See the [v0.2.0 release notes](release-notes/v0.2.0.md).

## v0.1.1

Makes SQL sessions report the authenticated MotherDuck identity on their first query and across pooled reconnects. See the [v0.1.1 release notes](release-notes/v0.1.1.md).

## v0.1.0

Manage MotherDuck SQL infrastructure and REST administration from Terraform. See the [v0.1.0 release notes](release-notes/v0.1.0.md).
