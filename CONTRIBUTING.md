# Contributing

This repository is a Terraform Plugin Framework provider. Keep changes small, schema-driven, and backed by the public MotherDuck REST API or public MotherDuck SQL/table-function behavior.

## Repository Layout

- `main.go`: provider entrypoint and version injection for release builds.
- `internal/provider`: provider schema, configuration, and resource/data-source registration.
- `internal/providerctx`: configured clients and shared provider runtime context.
- `internal/client/rest`: typed `net/http` client for MotherDuck REST administration APIs.
- `internal/client/sql`: `database/sql` client over `github.com/duckdb/duckdb-go/v2` for MotherDuck SQL.
- `internal/resources`: Terraform resources and import/state lifecycle logic.
- `internal/datasources`: Terraform data sources.
- `internal/sqlbuild`: SQL identifier quoting, literal escaping, and statement builders.
- `internal/motherduck/catalog.yaml`: public surface manifest that maps REST paths and SQL/table functions to provider surfaces.
- `internal/acceptance`: Terraform-backed live acceptance tests.
- `internal/dev`: local development helpers and smoke-test command code.
- `examples`: standalone Terraform examples used by generated docs and example validation.
- `docs`: generated provider docs plus hand-written guides and blueprint docs.
- `templates`: `tfplugindocs` templates for generated docs and guides.
- `scripts`: CI, release, Terraform-version, and live-smoke helpers.
- `test-fixtures`: Terraform fixtures for offline validation and live smoke tests.
- `.github/workflows`: pull-request, live-smoke, and release automation.

Code under `internal` is deliberately not a reusable public Go module. Treat it as provider implementation detail and prefer narrow package APIs over shared cross-package abstractions.

## Development Workflow

Run the local pre-push gate before pushing:

```bash
make pre-push-check
```

That gate runs formatting, `go vet`, Go tests, workflow linting, shell syntax checks, example validation, import diagnostics, invalid-configuration diagnostics, missing-credential diagnostics, REST helper checks, repository hygiene checks, and a provider build.

Regenerate docs after changing schemas, examples, or doc templates:

```bash
make docs
```

Run release packaging checks after changing release scripts or workflows:

```bash
make release-check
```

When updating Go modules, test the DuckDB/MotherDuck path before keeping a `duckdb-go` or `duckdb-go-bindings` bump. Newer embedded DuckDB builds can be published before MotherDuck supports that DuckDB version, so a clean `go test` is not enough; run at least `MOTHERDUCK_TOKEN=... make test-terraform-versions` or a focused live SQL smoke.

## Adding A Resource Or Data Source

1. Add typed client support in `internal/client/rest` or `internal/client/sql`.
2. Add Terraform implementation in `internal/resources` or `internal/datasources`.
3. Register the new surface in `internal/provider`.
4. Update `internal/motherduck/catalog.yaml`.
5. Add focused unit tests for validators, state transitions, SQL builders, REST behavior, and import parsing.
6. Add a standalone example under `examples/resources/<type>/` or `examples/data-sources/<type>/`.
7. Add or update live smoke coverage only when the surface creates durable remote behavior that unit tests cannot prove.
8. Run `make docs` and `make pre-push-check`.

Prefer Terraform resources for durable desired state. Avoid modeling one-off operational actions unless the state behavior is explicit and understandable in a Terraform plan.

## Testing

Offline checks:

```bash
make test-unit
make test-contract
make test-examples
make test-invalid-configuration
make test-missing-credentials
make workflow-check
```

The required live gate uses the SQL credential and fails rather than skipping when it is missing:

```bash
MOTHERDUCK_TOKEN=... make test-live-required
```

`make test-unit` uses the race detector, randomized ordering, and coverage summaries. `make test-contract` runs real Terraform protocol lifecycles against strict in-memory SQL and REST clients. Coverage is diagnostic; contracts gate observable state and backend side effects. Credentialed Go tests use the `acceptance` build tag and are only run by explicit live targets.

The hosted environment does not have an organization-admin token. REST administration behavior is covered by hermetic protocol contracts; admin-only Go acceptance tests require both the `acceptance` and `admin_acceptance` build tags, and admin-only live scripts must be run explicitly in an environment that supplies `MOTHERDUCK_ADMIN_TOKEN`. `make test-terraform-versions` and the hosted exact-main and release gates run SQL-only live coverage.

Use focused live smoke tests for changed surfaces instead of running every live fixture on every edit. The broad stable SQL gate is:

```bash
MOTHERDUCK_TOKEN=... make test-live-sql-stable
```

The Terraform compatibility matrix defaults to Terraform `1.5.7`, `1.8.5`, `1.12.2`, `1.15.8`, `1.15.9`, and `1.16.1`. Override locally with `TF_VERSIONS`, for example:

```bash
TF_VERSIONS="1.8.5 1.16.1" MOTHERDUCK_TOKEN=... make test-terraform-versions
```

Live smoke logs and temporary provider mirrors are written under ignored `test-results/` and `tools/`. Treat live logs as account metadata and do not paste raw output into public issues.

## CI

CI has three workflows:

- `.github/workflows/ci.yml`: pull-request and `main` checks. Static checks, hermetic behavior contracts, release packaging, and the offline Terraform compatibility matrix run as separate jobs.
- `.github/workflows/live-smoke.yml`: trusted exact-main, scheduled, and manual live checks using the protected `motherduck-live` environment. Missing credentials fail.
- `.github/workflows/release.yml`: tag-driven release workflow that requires the live SQL contract before building provider packages.

Actions are pinned to immutable SHAs with version comments. When updating an action, verify the latest upstream tag, replace the SHA, and run:

```bash
make workflow-check
make pre-push-check
```

## Release Process

Releases are created by pushing a semantic version tag:

```bash
git tag v0.1.0
git push origin v0.1.0
```

The release workflow runs the full preflight gate, builds native packages, creates checksums, signs the checksum file, adds the Terraform Registry manifest, and creates the GitHub release.

Repository release secrets:

- `GPG_PRIVATE_KEY`: private key used by the release workflow to sign checksum files.
- `GPG_PASSPHRASE`: passphrase for `GPG_PRIVATE_KEY`.

The provider embeds DuckDB through CGO, so release targets are built on native operating system runners. Add a platform only after proving that runner can build the package and Terraform can initialize the produced provider binary.

## Documentation Style

Customer-facing docs should describe stable public behavior, not private implementation notes. Public docs can mention preview feature gates, account/plan prerequisites, and Terraform state implications. Keep private readiness findings out of the public tree.
