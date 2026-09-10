---
page_title: "Provider architecture"
subcategory: "Contributing"
---

# Provider architecture

The provider uses Terraform Plugin Framework and protocol 6. The public source
address is `registry.terraform.io/motherduckdb/motherduck`.

## Runtime boundaries

| Directory | Responsibility |
| --- | --- |
| `internal/provider` | Provider schema, configuration, and registration |
| `internal/providerctx` | Shared clients and lazy SQL initialization |
| `internal/client/rest` | HTTP requests, bounded responses, pagination, safe retries |
| `internal/client/sql` | Embedded DuckDB, MotherDuck connection, serialized SQL |
| `internal/resources` | Resource state and create/read/update/delete/import behavior |
| `internal/datasources` | Read-only catalog and metadata projection |
| `internal/ephemeral`, `internal/diveembed` | Short-lived embed credentials |
| `internal/sqlbuild`, `internal/sqlcatalog` | SQL construction and shared catalog reads |
| `internal/retry`, `internal/tfvalidators` | Retry timing and input validation |
| `internal/motherduck` | Manifest of registered public surfaces |
| `internal/acceptance`, `internal/dev` | Live tests and development SQL helper |
| `examples`, `test-fixtures` | User examples and executable test configurations |
| `templates`, `docs` | Durable documentation inputs and generated reference |
| `scripts`, `.github/workflows` | Local gates, live smokes, CI, release packaging |

REST administration uses a separate admin credential. SQL clients initialize
lazily so REST-only operations do not need a SQL connection. SQL access is
serialized because operations can temporarily change the selected database.
Increasing the pool size without redesigning those session boundaries is unsafe.

When no database is configured, a SQL client connects to the saved workspace.
On a fresh empty account, that first connection can create the platform default
`my_db`. Configure an existing database explicitly and use single-database mode
when the connection must avoid workspace initialization.

Resources own Terraform state semantics. Clients should report service outcomes.
They should not decide that permission failures mean a Terraform resource was
deleted. New abstractions should have a concrete caller or test need.

## Change contracts

Public schemas are compatibility contracts. Preserve attribute names, types,
defaults, sensitivity, and import formats unless a planned breaking change is
documented. Use Framework state upgrades when a stored schema shape changes.
Changing the schema version alone does not migrate state.

Optional/computed values require special care: unknown plan values must not be
validated as empty strings, and refresh must not substitute server defaults in a
way that contradicts configured values. Test create, no-op refresh, import, drift,
and destroy using the real Terraform protocol.

Generated docs describe every registered surface. Edit schemas, examples, and
templates, then run `make docs`. Do not hand-edit generated reference pages.
