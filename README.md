# MotherDuck Terraform Provider

Terraform provider for managing MotherDuck organization resources and SQL-backed data infrastructure.

The provider uses the public MotherDuck REST API for administration resources and the public MotherDuck SQL/table-function surface for databases, schemas, tables, views, secrets, roles, shares, snapshots, Guides, Dives, and Flights.

## Quick Start

Use environment variables for credentials:

```bash
export MOTHERDUCK_TOKEN=...
export MOTHERDUCK_ADMIN_TOKEN=...
```

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

provider "motherduck" {}
```

`MOTHERDUCK_TOKEN` is used for SQL-backed resources and data sources. `MOTHERDUCK_ADMIN_TOKEN` is used for REST-backed organization administration, including service accounts, access tokens, active-account inspection, and Duckling configuration.

Keep tokens out of Terraform files and committed `.tfvars`. Terraform state can contain sensitive provider-managed values such as generated access tokens and share URLs, so use an encrypted remote backend with tightly scoped access.

## What The Provider Manages

REST-backed administration:

- `motherduck_service_account`
- `motherduck_access_token`
- `motherduck_duckling_config`
- `motherduck_active_accounts` and `motherduck_user_tokens` data sources
- [`motherduck_dive_embed_session` ephemeral resource](docs/ephemeral-resources/dive_embed_session.md), preferred for new configurations because it does not persist the session credential in Terraform state
- [`motherduck_dive_embed_session` data source](docs/data-sources/dive_embed_session.md), retained for compatibility but persists the sensitive session credential in Terraform state

SQL-backed infrastructure:

- `motherduck_database`, `motherduck_schema`, `motherduck_table`, and `motherduck_view`
- `motherduck_secret`
- `motherduck_role` and `motherduck_role_grant`
- `motherduck_share` and `motherduck_share_grant`
- `motherduck_snapshot`
- `motherduck_guide`
- `motherduck_dive`
- `motherduck_flight` and `motherduck_flight_run`

Catalog and environment data sources:

- databases, attached databases, snapshots, owned shares, incoming shares, and secrets
- current user, MotherDuck version, live Duckling size, object-storage buckets, files, roles, and role memberships
- Guides and Guide grantees, Dives, Flight definitions and owners, Flight versions, Flight runs, and Flight logs

Guide, Dive, Flight, and RBAC SQL resources and data sources are enabled by default. Function availability can vary by account, region, permissions, and client version; the provider checks required SQL functions before operations and returns an explicit diagnostic when a surface is not exposed.

## Blueprints

The repository includes reusable blueprint modules for common multi-tenant patterns:

- [Writer bootstrap](docs/blueprints/writer-bootstrap.md): the dedicated writer service account and read-write token that own tenant data infrastructure.
- [Hypertenancy](docs/blueprints/hypertenancy.md): one isolated database, schema, restricted share, reader service account, and reader token per tenant.
- [Read hypertenancy](docs/blueprints/read-hypertenancy.md): centralized writes through one writer identity, with per-tenant reader databases and restricted shares.

Use these as starting points for production modules. They keep tenant boundaries explicit, generate deterministic names, and avoid putting tenant tokens or share URLs into non-sensitive outputs. Run tenant data planes as the writer identity: MotherDuck databases are writable only by their owner, and only a share's owner can `GRANT READ ON SHARE` to readers.

## Provider Configuration

Common options:

- `database`: attach an existing database during provider SQL initialization.
- `attach_mode`: use `single` with `database` when the provider should avoid attaching every workspace database; use `workspace` or omit the argument for the default workspace behavior.
- `api_base_url`: defaults to `https://api.motherduck.com` or `MOTHERDUCK_API_BASE_URL`; set it only for controlled testing or proxying.
- `custom_user_agent`: adds a custom user agent suffix to both the DuckDB/MotherDuck SQL connection and MotherDuck REST API requests.
- `request_timeout_seconds`: REST API request timeout in seconds. Defaults to 30.

Provider configuration belongs in the root module. Reusable child modules should receive provider configurations from their caller.

## Native Packages

The provider embeds DuckDB through CGO. Release artifacts are native per-platform builds, so package size and platform availability follow the tested runner matrix: Linux amd64/arm64 and macOS amd64/arm64. Windows packages are not published until there is a tested native Windows CGO build path.

## Documentation

- [Generated provider docs](docs/index.md)
- [Terraform best practices](docs/guides/terraform-best-practices.md)
- [Testing overview](docs/guides/testing.md)
- [CI and release](docs/guides/ci-and-release.md)
- [Contributing](CONTRIBUTING.md)

Generated resource and data-source docs are built from the Terraform schema and examples under `examples/`.

## Development

Local development requires Go and Terraform. Run the default local gate before pushing:

```bash
make pre-push-check
```

Run the release packaging check before changing release automation:

```bash
make release-check
```

The release workflow is tag-driven. Pushing a semantic version tag such as `v0.1.0` runs the release preflight, builds provider packages, signs checksums, and creates the GitHub release.

See [Contributing](CONTRIBUTING.md) for the repository layout, test targets, CI policy, and release process.

## License

This repository is licensed under the [Apache License 2.0](LICENSE).
