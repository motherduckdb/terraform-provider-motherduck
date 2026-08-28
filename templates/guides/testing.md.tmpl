# Testing The Provider

This guide summarizes the test layers for users and contributors. The full contributor workflow lives in [Contributing](../../CONTRIBUTING.md).

## Offline Checks

Offline checks do not require MotherDuck credentials:

```bash
make pre-push-check
```

The pre-push gate runs Go formatting, Terraform formatting, `go vet`, Go tests, workflow linting, shell syntax checks, generated example validation, import diagnostics, invalid-configuration diagnostics, missing-credential diagnostics, REST helper checks, repository hygiene checks, and a provider build.

Use narrower targets while iterating:

```bash
make test-unit
make test-examples
make test-invalid-configuration
make test-missing-credentials
make workflow-check
```

`make test-examples` validates every Terraform directory under `examples/` against a fresh local provider mirror. Resource and blueprint examples also run offline plans with dummy values. Data-source examples stop at validation because Terraform reads data sources during planning.

Regenerate docs after changing schemas, templates, or examples:

```bash
make docs
```

## Live Checks

Live checks use credentials from environment variables:

- `MOTHERDUCK_TOKEN`: SQL and DuckDB-backed resources and data sources.
- `MOTHERDUCK_ADMIN_TOKEN`: REST-backed administration resources and data sources.

Run SQL integration and Terraform acceptance tests:

```bash
MOTHERDUCK_TOKEN=... make test-integration
MOTHERDUCK_TOKEN=... make test-acceptance
```

Add `MOTHERDUCK_ADMIN_TOKEN` for service-account, access-token, active-account, and Duckling-configuration coverage. The admin token must belong to a MotherDuck organization admin. Regular read-write tokens can run SQL checks but REST administration lifecycle tests will skip before creating infrastructure.

Run the broad stable SQL smoke before release candidates or larger SQL lifecycle changes:

```bash
MOTHERDUCK_TOKEN=... make test-live-sql-stable
```

Run focused live targets when changing a specific surface, for example:

```bash
MOTHERDUCK_TOKEN=... make test-live-sql-edge
MOTHERDUCK_TOKEN=... make test-live-sql-import
MOTHERDUCK_TOKEN=... make test-live-share-modes
MOTHERDUCK_ADMIN_TOKEN=... make test-live-rest-token-matrix
MOTHERDUCK_TOKEN=... MOTHERDUCK_ADMIN_TOKEN=... make test-live-blueprint-writer-path
```

`test-live-blueprint-writer-path` exercises the two-stage writer-ownership blueprint flow: it mints a writer service account and token with admin credentials, then applies tenant data infrastructure with the writer token as `MOTHERDUCK_TOKEN`, proving the writer owns the tenant database and can `GRANT READ ON SHARE` to a reader service account.

Preview and optional table-function checks can be required instead of skipped:

```bash
MD_TF_ACC_REQUIRE_DIVES=1 MOTHERDUCK_TOKEN=... make test-live-dive
MD_TF_ACC_REQUIRE_FLIGHTS=1 MOTHERDUCK_TOKEN=... make test-live-flight
MD_TF_ACC_REQUIRE_DIVE_FLIGHT_BLUEPRINT=1 MOTHERDUCK_TOKEN=... make test-live-dive-flight-blueprint
MD_TF_ACC_REQUIRE_OBJECT_STORAGE_LISTING=1 MOTHERDUCK_TOKEN=... make test-live-object-storage-listing
```

## Terraform Version Matrix

The compatibility matrix validates the provider across supported Terraform versions:

```bash
MOTHERDUCK_TOKEN=... make test-terraform-versions
```

The default matrix runs Terraform `1.5.7`, `1.8.5`, `1.12.2`, `1.15.8`, `1.15.9`, and `1.16.0`, plus OpenTofu `1.12.6`. Override either matrix locally with spaces or commas:

```bash
TF_VERSIONS="1.8.5 1.16.0" MOTHERDUCK_TOKEN=... make test-terraform-versions
TOFU_VERSIONS="1.12.6" MOTHERDUCK_TOKEN=... make test-terraform-versions
```

Add an admin token to include REST administration lifecycle coverage when the token has organization-admin permissions:

```bash
MOTHERDUCK_TOKEN=... \
MOTHERDUCK_ADMIN_TOKEN=... \
make test-terraform-versions
```

Use the lifecycle matrix when SQL create/destroy behavior must be proven across Terraform versions:

```bash
MOTHERDUCK_TOKEN=... make test-terraform-versions-lifecycle
```

Use the blueprint matrix when architecture modules should be proven across Terraform versions:

```bash
MOTHERDUCK_TOKEN=... make test-terraform-versions-blueprint
```

Both lifecycle matrices create durable MotherDuck objects while they run and clean them up on exit.

## Output And Cleanup

Live smoke fixtures write temporary Terraform directories, logs, local provider binaries, and provider mirrors under ignored `test-results/` and `tools/` paths. Treat live output as account metadata because it can include catalog names, share URLs, tenant names, and snapshot metadata even when Terraform masks sensitive values.

After interrupted live runs, audit common `tf_` leftovers:

```bash
MOTHERDUCK_TOKEN=... make test-live-cleanup-audit
```

To remove those common leftovers after an interrupted run:

```bash
MOTHERDUCK_TOKEN=... ./scripts/audit-live-test-cleanup.sh --sweep
```

Remove generated local provider binaries and mirrors when disk space gets tight:

```bash
make clean-generated
```

Remove downloaded Terraform CLIs and `tfplugindocs`:

```bash
make clean-tool-cache
```
