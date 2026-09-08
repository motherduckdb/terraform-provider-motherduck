---
page_title: "Testing The Provider"
subcategory: "Contributing"
---

# Testing The Provider

This guide summarizes the test layers for users and contributors. The full contributor workflow lives in [Contributing](https://github.com/motherduckdb/terraform-provider-motherduck/blob/main/CONTRIBUTING.md).

See the [fixture guide](../../test-fixtures/README.md) for fixture ownership and
[testing patterns](testing-patterns.md) for independent SQL checks,
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
make test-cli
make test-invalid-configuration
make test-missing-credentials
make workflow-check
```

`make test-unit` runs hermetic Go tests, including embedded DuckDB and direct ephemeral-resource checks, with the race detector, randomized ordering, a five-minute timeout, and package coverage summaries. Coverage is diagnostic information, not a line-percentage gate.

`make test-contract` runs only `TestContract*` tests in the provider package against the real Terraform protocol. It covers database/table/service-account lifecycles, import, drift repair, token secret preservation, Flight wait policy, nullable owned-share state, and SQL/REST read-error recovery. Unexpected backend operations and duplicate creates fail the test. A backend read error must produce a diagnostic and preserve existing state; recovery must plan no changes. Unit tests are not rerun in this target.

`make test-cli` builds the current provider once, then runs example validation/plans,
invalid-import diagnostics, invalid-configuration diagnostics, and missing-credential
checks. Each case has its own Terraform directory. The same executable is linked
into Terraform and OpenTofu filesystem mirrors; tests do not install a published
provider or implicitly reuse a previous build. Offline cases remove inherited
MotherDuck credentials, Terraform CLI arguments, variables, and provider reattachment
settings before executing.

For a narrower iteration, use `make test-examples`, `make test-import-validation`,
`make test-invalid-configuration`, or `make test-missing-credentials`. Each builds
its own current provider when run independently. `TERRAFORM_BIN` selects the CLI.
The invalid-configuration suite checks Terraform's JSON error diagnostics rather
than matching words from source snippets in rendered terminal output.

Example validation covers every Terraform directory under `examples/`. Resource
and blueprint examples also run offline plans with dummy values. Data sources and
ephemeral resources stop at validation because their plan phase calls the service.
This is intentionally not claimed as live or offline lifecycle coverage.

ShellCheck is required locally as well as in CI (`brew install shellcheck` on macOS,
or your platform's package manager). `make test-scripts` checks each shell file
individually and runs offline fault-injection tests for the harness: failed builds,
credential/CLI isolation, cleanup errors, EXIT status handling, and gate failures.

Regenerate docs after changing schemas, templates, or examples:

```bash
make docs
```

## Coverage and test design

| Layer | What it proves | What it does not prove |
| --- | --- | --- |
| Unit and embedded DuckDB | Validation, serialization, cancellation, SQL behavior | MotherDuck service availability |
| Protocol contracts | Database and service-account lifecycle/import/drift; table import/type/replacement; token secret preservation/import/recreation; owned-share state and read-error recovery | Live API permissions |
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

Admin-only live smoke scripts fail when the token authenticates but lacks organization-admin permissions. The Terraform version matrix remains SQL-only in that case, and the permission-diagnostics smoke uses the same response to test the provider's 403 diagnostic.

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

## Deploy the checked-in examples

Run the example deployment suites with SQL and organization-admin credentials
injected into the environment:

```bash
make test-live-examples
```

The role suite also needs a SQL token with role-administration privileges;
the REST admin token does not grant SQL permissions. The app suite requires
Terraform 1.10 or later for the ephemeral embed-session example.

For core object-storage coverage, inject `AWS_ACCESS_KEY_ID`,
`AWS_SECRET_ACCESS_KEY`, and optionally `AWS_SESSION_TOKEN`, and set
`MD_TF_ACC_OBJECT_STORAGE_PATH` to an S3 prefix containing `fixtures/orders.csv`.
The credentials need to read that fixture, list the bucket, and create/delete a
run-scoped probe below the prefix. Missing storage configuration reports
unavailable coverage; failures with supplied credentials fail the suite.

The suites create disposable service accounts and deploy the files under
`examples/`. They check the resulting state and permissions, refresh and no-op
plans, supported updates and imports, and destruction. Each suite supplies only
the prerequisites and unique names needed to isolate its examples. This catches
missing dependencies and invalid example configurations that offline validation
cannot detect.

Use the individual `test-live-examples-core`, `test-live-examples-roles`,
`test-live-examples-apps`, and `test-live-examples-warehouses` targets while
iterating. The aggregate target runs every group and returns nonzero if any group
fails or reports unavailable coverage. Review each group's results rather than
treating a skipped feature as a passing deployment.

Run these checks in a disposable test organization before release candidates or
changes to example composition, provider configuration, or resource lifecycles.
They can create compute and execute Flight code. State and logs stay under the
ignored `test-results/` directory and must not be published. Destroy data and
application objects before deleting their owning service accounts.

## Repeat lifecycle cycles

Run repeated plan/apply checks and alternating supported updates with:

```bash
make test-live-cycles
```

This uses the same disposable examples and credentials as `test-live-examples`.
By default, every successful resource apply is followed by five plan/apply
cycles. Managed-resource plans must remain empty, and IDs, creation metadata,
and Flight run numbers must remain stable. Supported app content, database,
view, share, snapshot-name, and Duckling configuration changes alternate across
five update cycles. The warehouse suite deliberately changes a disposable
token TTL once as a positive control for planned replacement.

Set `MD_CYCLE_REPEATS` and `MD_EXAMPLE_UPDATE_CYCLES` to positive integers to
change the repetition counts. Set `TF_TEST_PROVIDER_BINARY` to an absolute path
to a verified released provider binary and `PROVIDER_VERSION` to its version
when testing an immutable release rather than the current source build.

Private per-apply JSON reports contain resource identities and planned actions,
not credential values. They are saved under `test-results/lifecycle-cycles-*`.
Unavailable catalog capabilities still produce a nonzero aggregate result.

## Terraform Version Matrix

Run the offline matrix without credentials before changing CLI setup or fixtures:

```bash
make test-cli-versions
TF_VERSIONS="1.5.7 1.16.1" TOFU_VERSIONS="1.12.6" make test-cli-versions
```

It checksum-verifies CLI downloads and runs the same `test-cli` suites on each
selected CLI, sharing one provider build across the matrix. PR CI includes
OpenTofu as well as Terraform so CLI-specific validation errors are caught.

The live compatibility matrix additionally reads MotherDuck catalogs:

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

The SQL lifecycle matrix runs the database drop-with-objects smoke and the Guide, Dive, Flight, Dive-and-Flight blueprint, and share-grant drift smokes. Preview-surface smokes skip with a successful exit when the account lacks the required `MD_*` functions; set `MD_TF_ACC_REQUIRE_GUIDES`, `MD_TF_ACC_REQUIRE_DIVES`, `MD_TF_ACC_REQUIRE_FLIGHTS`, or `MD_TF_ACC_REQUIRE_DIVE_FLIGHT_BLUEPRINT` to fail instead of skipping.

Both lifecycle matrices create durable MotherDuck objects while they run and clean them up on exit.

## Output And Cleanup

The CLI suite prints one result per group and saves detailed logs under
`test-results/cli-<run-id>/`; failures print the failed group's log. Live smoke
fixtures retain Terraform directories and logs under ignored `test-results/`.
Provider binaries and symlink mirrors live under `tools/`. The stable live SQL
suite and CLI version matrix explicitly reuse one freshly built provider across
their child scripts. Resource-specific assertions stay in the individual scripts.

Live scripts use one EXIT cleanup path. Cleanup attempts all named fixture objects,
propagates failures, and preserves the original test's nonzero exit status. It
never turns a failed test or failed cleanup into success. An interrupted or failed
fixture retains state for diagnosis; it is not evidence that cleanup succeeded. Treat live output as account metadata because it can include catalog names, share URLs, tenant names, and snapshot metadata even when Terraform masks sensitive values.

After interrupted live runs, audit common `tf_` leftovers:

Each catalog read uses a fresh connection. Ordinary reads retain the two-minute
timeout, while the slower snapshot catalog has a five-minute budget. Reusing one
connection across all four reads caused repeated CI timeouts. Missing results,
extra results, and query failures fail the audit and identify the affected check.

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
