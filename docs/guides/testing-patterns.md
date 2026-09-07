# Write useful provider tests

For a SQL-backed resource, successful Terraform state does not prove that the
intended SQL changed the remote object. Check the plan, state, and observable
backend behavior independently.

## Lifecycle recipe

1. Generate a unique object name and keep all writes inside the fixture.
2. Apply minimal configuration. Assert state and inspect the remote object with
   a separate SQL client or API read.
3. Require an empty plan after refresh.
4. Import the object and compare state.
5. Change a mutable property and require an update action, not replacement.
6. Check the remote result and require another empty plan.
7. Import again to verify the updated shape.
8. Destroy and confirm the owned remote objects are gone.

`TestPluginTestingSQLObjectLifecycle` in `internal/acceptance/acceptance_test.go`
demonstrates this recipe. It creates a database, schema, table, and view, inserts
two fixture rows with a separate SQL client, updates the view to filter one row,
and checks row counts. The probe explicitly attaches the new database.

SQL has equivalent canonical spellings. If `ImportStateVerifyIgnore` excludes
a SQL attribute, add an `ImportStateCheck` that proves the recovered query's
behavior. The view test executes imported SQL against the fixture before and
after updates. Avoid broad ignore lists that hide important state.

## Choose the layer

Use hermetic protocol contracts for state transitions, exact writes, failures,
and deletion recovery. Use embedded DuckDB for conversion and session behavior.
Use live tests for deployed SQL semantics and permissions. Ordinary PR tests
must not need credentials. Authorization errors must not be treated as deletion.

List focused tests before running them to catch mistyped filters:

```shell
go test -tags=acceptance ./internal/acceptance -list '^TestPluginTestingSQLObjectLifecycle$'
TF_ACC=1 go test -tags=acceptance ./internal/acceptance -run '^TestPluginTestingSQLObjectLifecycle$' -count=1
```

Supply `MOTHERDUCK_TOKEN` through secret injection. After a focused test, run
`make test-live-cleanup-audit`. The complete `make test-live-required` target
includes the audit. A run matching zero tests does not prove coverage.

## Comparison with other providers

Selected public files reviewed September 6, 2026; this is not an exhaustive
assessment of those projects.

| Provider | Observed pattern | Application here |
| --- | --- | --- |
| Snowflake | Remote-object and Terraform-state assertions through create/import/update/re-import | Add independent SQL checks and update/import cycles |
| Google BigQuery | Table lifecycle sequences, remote checks, scoped import exclusions, VCR harness | Adopt the lifecycle checks using our existing SQL/HTTP seams |
| ClickHouse Cloud | Shared Make targets, generated docs checks, E2E upgrade inputs | Keep local/CI commands aligned; test upgrades once a published baseline exists |
| Databricks | Docs formatting, link integrity, schema comparison, separate unit/integration tests | Retain generated docs checks; review schema compatibility explicitly |

Sources:

- [Snowflake database tests](https://github.com/snowflakedb/terraform-provider-snowflake/blob/main/pkg/testacc/resource_database_acceptance_test.go)
- [BigQuery table tests](https://github.com/hashicorp/terraform-provider-google/blob/main/google/services/bigquery/resource_bigquery_table_test.go)
- [ClickHouse contributor workflow](https://github.com/ClickHouse/terraform-provider-clickhouse/blob/main/CONTRIBUTING.md)
- [ClickHouse E2E action](https://github.com/ClickHouse/terraform-provider-clickhouse/blob/main/.github/actions/e2e/action.yaml)
- [Databricks contributing guide](https://github.com/databricks/terraform-provider-databricks/blob/main/CONTRIBUTING.md)

Snowflake is the closest model for SQL objects, but its large assertion/config
libraries are unnecessary for this provider. BigQuery's generated provider and
HTTP recording solve different scale and transport problems. ClickHouse Cloud
primarily manages cloud services and points database access control to another
provider.

## Test the failure paths

`internal/provider/read_failure_contract_test.go` demonstrates a failed refresh
followed by recovery. Inject an SQL error or REST 403, require the diagnostic,
restore the backend, then require an empty plan and the original resource/token
identity. A failed read is not proof that the object disappeared. Strict backends
reject a duplicate create, a wrong lookup argument, or an unrelated delete.

Keep shell scenarios explicit. Use `scripts/lib/terraform-test.sh` for provider
build/mirror setup and `scripts/lib/live-common.sh` for cleanup. Register
`trap live_cleanup_on_exit EXIT` once and let it own final cleanup. Within a
cleanup function, collect each failure with `|| destroy_status=$?` so later
resources are still attempted, then return the accumulated status. Do not use
`|| true` to discard cleanup errors.

Harness behavior belongs in small subprocess tests with fake commands. The shell
unit tests inject a build failure, an invalid later script, a failed docs generator,
and cleanup failures to prove the gates themselves fail. These tests never use
MotherDuck credentials.

## CI and upgrade policy

Keep PR gates deterministic, credential-free, and bounded. Native package jobs
own artifact installation; static checks should not duplicate them. Trusted main
checks own live SQL behavior.

Once an immutable first release exists, add old-provider create → new-provider
refresh/no-op → update → import → destroy tests. Do not invent a published
baseline. For public schema changes, compare attribute types, defaults,
sensitivity, and import behavior, and test state upgrades where needed.

See Terraform's [testing patterns](https://developer.hashicorp.com/terraform/plugin/testing/testing-patterns)
and [import tests](https://developer.hashicorp.com/terraform/plugin/testing/acceptance-tests/import-mode)
for the upstream lifecycle and comparison mechanisms.
