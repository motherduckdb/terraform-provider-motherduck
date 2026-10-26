# Contributing

This repository is a Terraform Plugin Framework provider. Keep changes small, schema-driven, and backed by the public MotherDuck REST API or public MotherDuck SQL and table-function behavior.

Contributor guides:

- [Provider architecture](docs/guides/provider-architecture.md): package layout and runtime boundaries.
- [Local development](docs/guides/local-development.md): build and run the provider from source.
- [Test the provider](docs/guides/testing.md): test layers, Make targets, and live credentials.
- [Testing patterns](docs/guides/testing-patterns.md): how to write lifecycle, import, and failure tests.
- [CI and release](docs/guides/ci-and-release.md): workflows, jobs, and the tested Terraform and OpenTofu versions.
- [Release checklist](docs/guides/release-readiness.md): steps before and after tagging.

## Repository layout

- `main.go`: provider entrypoint. Release builds set the version here.
- `internal/`: provider implementation. It is not a reusable Go module. Prefer narrow package APIs over shared cross-package abstractions.
- `examples/`: standalone Terraform examples used by generated docs and example validation.
- `templates/`: `tfplugindocs` templates for the provider index, resources, and guides.
- `docs/`: generated docs. Do not edit by hand.
- `scripts/`: CI, release, CLI-matrix, and live-smoke helpers.
- `test-fixtures/`: Terraform fixtures for offline validation and live smokes.
- `release-notes/`: one user-facing notes file per release tag.

Resource implementations and their tests use `<name>_resource.go` and
`<name>_resource_test.go`. Catalog data source definitions live in
`internal/datasources/catalog_specs.go`. Each `scripts/test-*.sh` listed in
`SCRIPT_TEST_SCRIPTS` in the Makefile has a matching `make` target.

## Development workflow

Install Go (the toolchain in `go.mod`), a C/C++ compiler, Terraform, ShellCheck,
and `gpg` (`brew install shellcheck gnupg` on macOS). Then:

```bash
make pre-push-check   # required before pushing
make docs             # after changing schemas, examples, or templates
make release-check    # after changing release scripts or workflows
```

When bumping `github.com/duckdb/duckdb-go/*` or `github.com/duckdb/duckdb-go-bindings*`,
a clean `go test` is not enough, because a new embedded DuckDB can ship before
MotherDuck supports it. Dependabot ignores these modules. Bump them manually in a
dedicated PR, run `MOTHERDUCK_TOKEN=... make test-terraform-versions` or a focused
live SQL smoke, and record the result in the PR description.

GitHub Actions are pinned to commit SHAs with version comments. When updating one,
check the latest upstream tag, replace the SHA, and run `make workflow-check`.

## Adding a resource or data source

1. Add client support in `internal/client/rest` or `internal/client/sql`.
2. Add the implementation in `internal/resources` or `internal/datasources`.
3. Register it in `internal/provider`.
4. Update `internal/motherduck/catalog.yaml`.
5. Add unit tests for validators, state transitions, SQL builders, REST behavior, and import parsing, plus a protocol contract for the lifecycle.
6. Add an example under `examples/resources/<type>/` or `examples/data-sources/<type>/`.
7. Add live smoke coverage only when the surface has remote behavior that hermetic tests cannot prove.
8. Run `make docs` and `make pre-push-check`.

Prefer resources for durable desired state. Avoid modeling one-off operational
actions unless their state behavior is clear in a Terraform plan.

## Writing

Do not use semicolons or em dashes in authored prose, comments, descriptions, or
user-facing messages. Preserve required code and SQL syntax, protocol values,
parser test inputs, and the license text.

## Documentation

Keep the provider introduction in `templates/index.md.tmpl` and guides in
`templates/guides/`. Each guide needs a `page_title` and a `subcategory` in its
frontmatter. Resource templates add usage and lifecycle notes around the
generated schema and example snippets.

Architecture diagrams have PNG and SVG outputs under `templates/assets/`. Edit
`scripts/render-docs-diagrams.py`, run it with Python and Pillow, then run
`make docs`. Install the Inter font first so the PNGs match the SVGs. The script
falls back to Arial or DejaVu Sans. Keep the shared visual grammar: dashed purple lines are Terraform
provisioning, solid arrows are data or code movement, blue lines are read access,
every arrow has a short verb, and each diagram has a legend for the line styles
it uses. Update the guide's alt text when a diagram changes. PNG embeds use
absolute raw GitHub URLs, so they render only after the assets reach `main`.

`make docs-check` regenerates docs, fails on drift, and runs
`tfplugindocs validate`. Registry documentation updates ship with the next
provider release.

Public docs describe stable public behavior. They can mention preview feature
gates, plan prerequisites, and Terraform state implications. Keep private
readiness notes out of the public tree.
