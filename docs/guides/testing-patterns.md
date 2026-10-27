---
page_title: "Write useful provider tests"
subcategory: "Contributing"
description: |-
  Patterns for provider tests that verify remote behavior, state, and imports.
---

# Write useful provider tests

Successful Terraform state does not prove that the intended SQL or API call
changed the remote object. Check the plan, the state, and the backend
independently. See [Test the provider](testing.md) for the commands that run
each layer.

## Choose the layer

- Hermetic protocol contracts (`internal/provider/*_contract_test.go`): state transitions, exact writes, failures, and recovery.
- Embedded DuckDB unit tests: SQL conversion and session behavior.
- Live tests: deployed SQL semantics and permissions.

Ordinary PR tests must not need credentials. A missing credential or a skipped
optional feature is not coverage.

## Lifecycle recipe

1. Generate a unique object name and keep all writes inside the fixture.
2. Apply a minimal configuration. Assert state, then inspect the remote object
   with a separate SQL client or API read.
3. Require an empty plan after refresh.
4. Import the object and compare state.
5. Change a mutable property and require an update, not a replacement.
6. Check the remote result and require another empty plan.
7. Import again to verify the updated shape.
8. Destroy and confirm the owned remote objects are gone.

Contracts should also cover external deletion and assert exact backend writes so
a duplicate create fails.

`TestPluginTestingSQLObjectLifecycle` in `internal/acceptance/acceptance_test.go`
follows this recipe. It creates a database, schema, table, and view, inserts
fixture rows with a separate SQL client, updates the view filter, and checks row
counts.

SQL has equivalent canonical spellings. If `ImportStateVerifyIgnore` excludes a
SQL attribute, add an `ImportStateCheck` that proves the imported query behaves
the same. Avoid broad ignore lists.

List a focused test before running it so a mistyped filter does not pass with
zero tests:

```shell
go test -tags=acceptance ./internal/acceptance -list '^TestPluginTestingSQLObjectLifecycle$'
MOTHERDUCK_TOKEN=... TF_ACC=1 go test -tags=acceptance ./internal/acceptance -run '^TestPluginTestingSQLObjectLifecycle$' -count=1
```

After a focused live test, run `make test-live-cleanup-audit`.

## Failure paths

`internal/provider/read_failure_contract_test.go` shows a failed refresh followed
by recovery. Inject an SQL error or REST 403, require the diagnostic, restore the
backend, then require an empty plan and the original resource identity. A failed
read is not proof that the object is gone, so never treat an authorization error
as deletion. Strict backends reject a duplicate create, a wrong lookup argument,
or an unrelated delete.

Regression tests should fail on the original bug. Use benchmarks for performance
changes, not wall-clock thresholds in PR gates.

## Shell scenarios

Use `scripts/lib/terraform-test.sh` for provider build and mirror setup and
`scripts/lib/live-common.sh` for cleanup. Register
`trap live_cleanup_on_exit EXIT` once and let it own final cleanup. In a cleanup
function, collect each failure with `|| destroy_status=$?` so later resources are
still attempted, then return the accumulated status. Do not use `|| true` to
discard cleanup errors.

Test harness behavior with small subprocess tests and fake commands, as the
`scripts/test-*-unit.sh` files do. These never use credentials.

## Schema changes and upgrades

For public schema changes, compare attribute types, defaults, sensitivity, and
import behavior, and add a state upgrader when the stored shape changes. To test
an upgrade, create with the previous release, refresh with the new build and
expect no changes, then update, import, and destroy.

Upstream references:
[testing patterns](https://developer.hashicorp.com/terraform/plugin/testing/testing-patterns)
and [import tests](https://developer.hashicorp.com/terraform/plugin/testing/acceptance-tests/import-mode).
