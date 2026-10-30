---
page_title: "Test the provider"
subcategory: "Contributing"
description: |-
  Offline, contract, and live test layers and the commands that run them.
---

# Test the provider

This page lists the test layers and the commands that run them. See
[testing patterns](testing-patterns.md) for how to write a new test and the
[fixture guide](https://github.com/motherduckdb/terraform-provider-motherduck/blob/main/test-fixtures/README.md)
for fixture ownership.

## Layers

| Layer | Command | Proves | Does not prove |
| --- | --- | --- | --- |
| Unit and embedded DuckDB | `make test-unit` | Validation, serialization, cancellation, SQL behavior | MotherDuck service behavior |
| Protocol contracts | `make test-contract` | Lifecycle, import, drift, read-error recovery, exact backend writes | Live API permissions |
| CLI compatibility | `make test-cli`, `make test-cli-versions` | Examples and diagnostics on each supported CLI | Resource lifecycles on every CLI |
| Native packages | `make release-check` | ZIP layout, mirror install, plugin startup, schema | Registry signing |
| Required live SQL | `make test-live-required` | Database, schema, table, and view lifecycles and imports, cleanup | Every SQL resource or REST administration |
| Focused live smokes | `make test-live-<surface>` | The selected surface and its cleanup | Anything that fixture does not exercise |

Coverage percentages are reported but not gated. Contracts gate observable state
and backend side effects instead.

## Offline checks

None of these need MotherDuck credentials. Run the full gate before pushing:

```bash
make pre-push-check
```

It runs `make static-check`, `make test-unit`, and `make test-contract`. Narrower
targets:

```bash
make test-unit
make test-contract
make test-cli
make test-examples
make test-import-validation
make test-invalid-configuration
make test-missing-credentials
make test-scripts
make workflow-check
```

- `make test-unit` runs all Go tests with the race detector, shuffled order, and a five-minute timeout.
- `make test-contract` runs only `TestContract*` tests in `internal/provider` against the real Terraform protocol with strict in-memory SQL and REST backends. Unexpected or duplicate backend operations fail the test.
- `make test-cli` builds the provider once and runs example validation, invalid-import, invalid-configuration, and missing-credential checks, each in its own directory. Offline cases strip inherited credentials and Terraform settings. `TERRAFORM_BIN` selects the CLI. Every example is validated. Resource and blueprint examples also run offline plans. Data sources and ephemeral resources stop at validation because planning them calls the service.
- `make test-scripts` checks shell syntax and runs fault-injection tests for the harness itself. It needs `gpg`.

ShellCheck and `gpg` are required locally (`brew install shellcheck gnupg` on macOS).

Run the offline CLI matrix after changing CLI setup or fixtures:

```bash
make test-cli-versions
TF_VERSIONS="1.5.7 1.16.4" TOFU_VERSIONS="1.12.6" make test-cli-versions
```

The default versions are listed in [CI and release](ci-and-release.md).

## Live credentials

- `MOTHERDUCK_TOKEN`: SQL-backed resources and data sources.
- `MOTHERDUCK_ADMIN_TOKEN`: optional organization-admin token for REST administration resources and data sources.

Live targets fail when a required token is missing. Admin-only scripts also fail
when the token lacks organization-admin permission. `test-live-share-grant-drift`
is the exception. It skips without `MOTHERDUCK_ADMIN_TOKEN`. Use a disposable
test organization.

## Required live gate

```bash
MOTHERDUCK_TOKEN=... make test-live-required
```

This runs the SQL client integration tests (`make test-integration`), the
Terraform acceptance tests (`make test-acceptance`), and the snapshot recovery
tests, then audits cleanup. Admin acceptance tests need the `admin_acceptance`
build tag and run through `make test-live-admin`.

## Focused live smokes

Run the smoke for the surface you changed. Every `scripts/test-live-*.sh` listed
in `SCRIPT_TEST_SCRIPTS` in the Makefile has a matching target. Examples:

```bash
MOTHERDUCK_TOKEN=... make test-live-sql-stable
MOTHERDUCK_TOKEN=... make test-live-sql-import
MOTHERDUCK_TOKEN=... make test-live-share-modes
MOTHERDUCK_ADMIN_TOKEN=... make test-live-rest-token-matrix
MOTHERDUCK_TOKEN=... MOTHERDUCK_ADMIN_TOKEN=... make test-live-blueprint-writer-path
```

`test-live-sql-stable` is the broad SQL smoke for release candidates and larger
SQL changes. `test-live-blueprint-writer-path` mints a writer service account
with the admin token, then applies tenant infrastructure as that writer and
checks that it can grant a share to a reader.

Preview-surface smokes skip with a successful exit when the account lacks the
required functions. Set one of these to fail instead:

- `MD_TF_ACC_REQUIRE_GUIDES`
- `MD_TF_ACC_REQUIRE_DIVES`
- `MD_TF_ACC_REQUIRE_FLIGHTS`
- `MD_TF_ACC_REQUIRE_DIVE_FLIGHT_BLUEPRINT`
- `MD_TF_ACC_REQUIRE_OBJECT_STORAGE_LISTING`

Other plan-gated targets: `make test-live-iceberg` (needs
`MOTHERDUCK_ICEBERG_SECRET` and `MOTHERDUCK_ICEBERG_DEFAULT_SCHEMA`) and
`make test-live-secure-views` (Business or Enterprise plan, or an active trial).

## Live CLI matrix

```bash
MOTHERDUCK_TOKEN=... make test-terraform-versions
MOTHERDUCK_TOKEN=... make test-terraform-versions-lifecycle
MOTHERDUCK_TOKEN=... make test-terraform-versions-blueprint
```

`test-terraform-versions` reads catalogs on each version. Set
`MOTHERDUCK_ADMIN_TOKEN` to add REST administration coverage. The `-lifecycle`
target adds SQL create and destroy smokes, and `-blueprint` adds the blueprint
modules. `TF_VERSIONS` and `TOFU_VERSIONS` accept spaces or commas.

## Deploy the checked-in examples

```bash
make test-live-examples
```

This deploys the checked-in examples with `MOTHERDUCK_TOKEN` and
`MOTHERDUCK_ADMIN_TOKEN`, then checks state, permissions, no-op plans, updates, imports, and destroy. Use
`test-live-examples-core`, `-roles`, `-apps`, or `-warehouses` while iterating.
The aggregate target fails if any group fails or reports unavailable coverage.

- The role suite needs a SQL token with role-administration privileges. The admin token does not grant SQL permissions.
- The app suite needs Terraform 1.10 or later.
- Core object-storage coverage needs `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, optional `AWS_SESSION_TOKEN`, and `MD_TF_ACC_OBJECT_STORAGE_PATH` set to an S3 prefix containing `fixtures/orders.csv`.

These suites create compute and run Flight code. Run them in a disposable
organization before release candidates or changes to examples, provider
configuration, or resource lifecycles.

Repeat plan and apply cycles over the same examples with:

```bash
make test-live-cycles
```

Each apply is followed by repeated plans that must stay empty with stable IDs,
and supported updates alternate across cycles. `MD_CYCLE_REPEATS` and
`MD_EXAMPLE_UPDATE_CYCLES` change the counts. To test a released binary instead
of the source build, set `TF_TEST_PROVIDER_BINARY` to its absolute path and
`PROVIDER_VERSION` to its version.

## Pulumi and example runtimes

These targets check the Pulumi bridge and the application code in the examples.
Pull-request CI runs `make test-pulumi`, and Live Smoke runs
`make test-live-pulumi` on `main`. CI runs none of the others, so run them
before merging a dependency update for those examples.

```bash
make test-pulumi                                      # offline Pulumi schema and example checks
make test-example-backend                             # customer-facing analytics backend build and tests
MOTHERDUCK_TOKEN=... make test-live-pulumi            # admin program runs when MOTHERDUCK_ADMIN_TOKEN is set
MOTHERDUCK_TOKEN=... make test-live-pulumi-example    # the checked-in Pulumi Python example
MOTHERDUCK_TOKEN=... MOTHERDUCK_ADMIN_TOKEN=... make test-live-cookbook-pipeline  # through uv
MOTHERDUCK_TOKEN=... MOTHERDUCK_ADMIN_TOKEN=... make test-live-role-audit
```

## Output and cleanup

Logs, state, and Terraform directories go under the ignored `test-results/`
directory. Provider builds and mirrors go under `tools/`. Treat live output as
account metadata and do not publish it.

Live scripts register one EXIT cleanup that attempts every fixture object and
keeps the original nonzero exit status. After an interrupted run, audit and
remove leftover `tf_` objects:

```bash
MOTHERDUCK_TOKEN=... make test-live-cleanup-audit
MOTHERDUCK_TOKEN=... ./scripts/audit-live-test-cleanup.sh --sweep
```

The sweep unnames `tf_` snapshots, drops `tf_` shares, databases, and secrets,
then audits again and fails if any remain.

Free disk space:

```bash
make clean-generated     # local provider binaries and mirrors
make clean-tool-cache    # downloaded Terraform, OpenTofu, and tools/bin
make clean-test-results  # per-run logs and working directories under test-results/
```

Regenerate docs after changing schemas, templates, or examples with `make docs`.
