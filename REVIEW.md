# Provider review: September 5, 2026

Baseline: `2ad3cb6e12b2ec7d66c081db0db68945580c2eb7`.

## Highest-priority changes implemented

1. **Restore database selection after cancellation.** `WithDatabaseUse` restored
   the previous database using the canceled operation context. A real embedded
   DuckDB reproduction left `current_database()` set to the temporary database,
   affecting subsequent operations on the shared connection. Restoration now
   has an independent, bounded context. Failure to read the original database
   also returns an error instead of guessing `memory`.
2. **Preserve exact catalog numbers in Terraform state.** Typed catalog decoding
   converted JSON numbers through `float64`. For example, `9007199254740993`
   became `9.007199254740992e+15`. Decode each field as `json.RawMessage` and
   unquote strings without converting numeric values. Existing raw JSON and
   sensitive field schemas remain available.
3. **Bound Flight status queries by the wait timeout.** The polling loop checked
   wall-clock time between queries but passed an unbounded context into SQL.
   A slow status query could exceed `timeout_seconds`. The configured timeout
   now propagates to status queries and retries through the operation context.
4. **Remove repeated catalog scan work.** Allocate scan buffers and normalize
   column metadata once per result set. A three-run local DuckDB benchmark over
   1,000 rows reduced allocations from approximately 18,827 to 15,830 per read
   (16%) and memory from 749 KB to 701 KB. Observed time fell from roughly
   0.92–0.95 ms to 0.84–0.87 ms. These are local measurements, not a guarantee
   of equivalent gains for network-bound MotherDuck workloads.
5. **Remove duplicate retry sleep code.** REST requests now reuse `retry.Sleep`,
   retaining one implementation of cancellation-aware delay handling.

## Verification

- All three bug checks fail against the baseline and pass after the changes.
- Embedded DuckDB tests verify cancellation restoration and distinct row/null
  values after scan-buffer reuse.
- `make pre-push-check` passes, including race tests, Terraform protocol
  contracts, lint, vulnerability checks, generated docs, and examples.
- `make test-live-required` passes against MotherDuck, including SQL integration,
  Terraform acceptance, and an empty cleanup audit.
- Reproduce the performance measurement with
  `go test ./internal/client/sql -run '^$' -bench BenchmarkQueryRowsJSON -benchmem`.

## Next priorities

- **Cancelable SQL admission.** The shared SQL mutex serializes access correctly
  but acquiring it does not observe context cancellation. An expired operation
  can still wait behind another operation before reaching `database/sql`.
  Address this with a concurrency contract before changing connection ownership.
- **Consolidate smoke fixture setup.** Live shell scripts repeat provider build,
  mirror, CLI configuration, and work-directory setup. Extract those mechanics
  into the existing shell library incrementally, preserving each fixture's
  explicit ownership and cleanup behavior.
- **Repair historical release evidence before first publication.** Checked-in
  release notes reference history predating the public repository migration.
  Audit their links and release claims against the public repository when
  preparing the first release.

The review examined shared clients, provider configuration, SQL/REST resource
lifecycle paths, catalog conversion, polling, and test/release infrastructure.
It is not an exhaustive security audit or proof that every resource is bug-free.

## Release preparation follow-up

The next pass adds service-account lifecycle and table-import protocol contracts,
race-enabled contract execution, bounded CI jobs, native ZIP installation and
schema discovery on each advertised platform, and manifest validation. The Intel
macOS build now uses an Intel runner. Release signing verifies all four artifacts
and the generated signature, without passing the passphrase on the command line.

Documentation now includes setup, development, architecture, state/import
semantics, and an explicit release-readiness checklist. Registered surfaces must
have example/schema documentation. Resources must explain import behavior.
Stale notes for unpublished versions were removed and the initial candidate
notes were linked to current public history.

Registry publication and live administration verification remain separate
release prerequisites. See docs/guides/release-readiness.md.
