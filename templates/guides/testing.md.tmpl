# Testing The Provider

This guide summarizes the test layers for users and contributors. The full contributor workflow lives in [Contributing](../../CONTRIBUTING.md).

See [testing patterns](testing-patterns.md) for independent SQL checks,
update/import cycles, and the comparison with other database providers.

## Offline Checks

Offline checks do not require MotherDuck credentials:

```bash
make pre-push-check
```

The pre-push gate combines static checks with hermetic behavior contracts. It runs formatting, linting, vulnerability checks, workflow and shell validation, generated example and documentation checks, provider builds, race-enabled Go tests, and Terraform protocol lifecycle tests against strict in-memory SQL and REST clients.

Use narrower targets while iterating:

```bash
make test-unit
make test-contract
make static-check
make test-examples
make test-invalid-configuration
make test-missing-credentials
make workflow-check
```

`make test-unit` runs hermetic Go tests with the race detector, randomized ordering, and package coverage summaries. Coverage is diagnostic information, not a line-percentage gate.

`make test-contract` runs the real Terraform protocol lifecycle against strict fake backends. The contracts cover database create/refresh/import/deletion/recreation/destroy, table type canonicalization and drift replacement, access-token secret preservation and deletion recovery, typed nullable owned-share state, and ephemeral Dive embed sessions. Unexpected backend operations fail the test.

`make test-examples` validates every Terraform directory under `examples/` against a fresh local provider mirror. Resource and blueprint examples also run offline plans with dummy values. Data-source examples stop at validation because Terraform reads data sources during planning.

Regenerate docs after changing schemas, templates, or examples:

```bash
make docs
```

## Coverage and test design

| Layer | What it proves | What it does not prove |
| --- | --- | --- |
| Unit and embedded DuckDB | Validation, serialization, cancellation, SQL behavior | MotherDuck service availability |
| Protocol contracts | Database and service-account lifecycle/import/drift; table import/type/replacement; token secret preservation/import/recreation; owned-share and ephemeral state | Live API permissions |
| CLI compatibility | Examples and diagnostics across supported Terraform versions | Every resource lifecycle on every CLI |
| Native packages | ZIP layout, mirror installation, plugin startup, schema, validation on four platforms | Registry signing or discovery |
| Required live SQL | Database/schema/table/view lifecycles and imports, in-place view updates, execution of imported SQL, cleanup | All SQL resources or REST administration |
| Focused live smokes | The selected service behavior and cleanup | Surfaces not exercised by that fixture |

Contracts run with race detection, randomized ordering, and a five-minute test
timeout. For a new resource, assert initial creation, an empty plan after refresh,
imported state, configuration updates or replacement, external deletion, and
final remote cleanup. Assert exact writes so accidental duplicate creates fail.
Use a strict local HTTP server for REST and the existing SQL seam for protocol
tests. Never count a missing credential or skipped optional feature as coverage.

Regression tests should fail on the original bug. Use benchmarks to establish
performance changes; do not use noisy wall-clock thresholds as ordinary PR gates.

## Live test credentials

Live checks use credentials from environment variables:

- `MOTHERDUCK_TOKEN`: SQL and DuckDB-backed resources and data sources.
- `MOTHERDUCK_ADMIN_TOKEN`: optional organization-admin coverage for REST-backed administration resources and data sources. Hosted gates do not have this credential.

Run the required SQL lifecycle gate:

```bash
MOTHERDUCK_TOKEN=... make test-live-required
```

All three targets require `MOTHERDUCK_TOKEN` and fail when it is missing. `test-integration` runs the Go SQL-client integration tests, `test-acceptance` runs Terraform provider lifecycles, and `test-live-required` runs both followed by a cleanup audit. REST administration behavior is required in the hermetic protocol suite; admin-only Go acceptance tests additionally require the `admin_acceptance` build tag and remain optional because the hosted environment has no organization-admin token.

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

The default matrix runs Terraform `1.5.7`, `1.8.5`, `1.12.2`, `1.15.8`, `1.15.9`, and `1.16.1`, plus OpenTofu `1.12.6`. Override either matrix locally with spaces or commas:

```bash
TF_VERSIONS="1.8.5 1.16.1" MOTHERDUCK_TOKEN=... make test-terraform-versions
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
