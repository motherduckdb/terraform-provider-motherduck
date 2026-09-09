# Terraform test fixtures

These are inputs to tests, not deployable examples. Copyable configurations live
in [examples](../examples). Tests copy fixtures into an isolated working directory.
Never run `terraform apply` directly in this directory.

| Fixture | Owner and purpose |
| --- | --- |
| `invalid-*`, `deferred-configuration` | Offline CLI diagnostics and unknown-value validation, run by `make test-cli` |
| `missing-*-token` | Offline tests proving missing credentials fail before service access |
| `read-only-smoke` | Credentialed Terraform/OpenTofu matrix, catalog reads only |
| `live-*` | Focused scripts in `scripts/test-live-*.sh`, resource lifecycle, drift, permissions, and cleanup |

The fixture name usually matches its script. Imported-state fixtures are paired
with their source fixture by the `test-live-*-import.sh` scripts. The writer
bootstrap/tenant fixtures are paired by `test-live-blueprint-writer-path.sh`.
Scenario-specific variables, SQL mutations, plan assertions, and ownership stay
in that script so the whole test is readable in one place.

Run `make test-cli` for all offline checks. It builds one provider from the current
checkout and installs that binary into local mirrors. Use `TERRAFORM_BIN` to test
with a different Terraform or OpenTofu executable. No MotherDuck credentials are
used by these checks.

Live scripts require explicitly supplied credentials and create disposable
objects with unique names. They retain their work directories under ignored
`test-results/` for diagnosis. Their EXIT handler attempts cleanup even after a
test failure, propagates cleanup errors, and preserves the original failure.
Inspect state and the cleanup result before deleting a failed run's directory.

For a new behavior, prefer a small unit test or a
[Terraform protocol contract](../internal/provider/contracts_test.go) when it can
be proved offline. Add a live fixture when deployed SQL semantics or permissions
are the behavior under test. See the [testing guide](../docs/guides/testing.md) and
[lifecycle recipe](../docs/guides/testing-patterns.md).
