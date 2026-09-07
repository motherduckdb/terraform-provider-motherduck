# MotherDuck Terraform Provider

Manage MotherDuck infrastructure with Terraform: SQL-backed databases, schemas,
tables, views, shares, roles, secrets, and snapshots,
plus REST-backed service accounts, access tokens, and Duckling configuration.

Distribution: [GitHub Releases](https://github.com/motherduckdb/terraform-provider-motherduck/releases).
**This provider is not published in the Terraform Registry.**
Its Terraform source address remains `registry.terraform.io/motherduckdb/motherduck`
for identity and future compatibility; install it through a filesystem mirror.
Built with Terraform Plugin Framework, protocol 6, and embedded DuckDB.

## Recommended ownership

Use Terraform to provision infrastructure and access controls. Use your application
deployment pipeline to publish Flight code, Dive content, and Guides, then trigger
executions through the CLI or SQL interface.

Flight definitions and schedules remain an optional Terraform deployment path
when Git/Terraform is their sole owner. Dives and Guides are experimental provider
surfaces, outside the first release's stable support commitment. Flight-run
resources are deprecated. Existing configurations remain supported for migration;
see [resource scope and migration](docs/guides/resource-scope.md).

## Start here

| Task | Read |
| --- | --- |
| Install a GitHub release | [GitHub installation](docs/guides/github-installation.md) |
| Create your first database | [Walkthrough](docs/guides/getting-started.md) |
| Build a warehouse with service-account environments | [Warehouse examples](examples/warehouses/README.md) |
| Run this source before Registry publication | [Local development](docs/guides/local-development.md) |
| Write production Terraform configuration | [Terraform best practices](docs/guides/terraform-best-practices.md) |
| Import objects, handle drift, or protect state | [State and lifecycle](docs/guides/state-and-lifecycle.md) |
| Find attributes, defaults, and examples | [Provider reference](docs/index.md), [resources](docs/resources), [data sources](docs/data-sources) |
| Change the provider | [Agent instructions](AGENTS.md), [contributing](CONTRIBUTING.md), [architecture](docs/guides/provider-architecture.md) |
| Add or select tests | [Testing](docs/guides/testing.md), [testing patterns](docs/guides/testing-patterns.md) |
| Prepare a release | [CI and release](docs/guides/ci-and-release.md), [release readiness](docs/guides/release-readiness.md) |

## Using the provider

Download a version from [GitHub Releases](https://github.com/motherduckdb/terraform-provider-motherduck/releases)
and follow the [filesystem mirror installation guide](docs/guides/github-installation.md)
before running `terraform init`. Terraform does not automatically download this
provider from GitHub based on its source address.

After installing the package in your mirror, use this root-module configuration:

```hcl
terraform {
  required_version = ">= 1.5.0"

  required_providers {
    motherduck = {
      source  = "motherduckdb/motherduck"
      version = "~> 0.1.0"
    }
  }
}

provider "motherduck" {}

resource "motherduck_database" "analytics" {
  name = "analytics"
}
```

Supply credentials through your secret manager or environment:

| Credential | Used for |
| --- | --- |
| `MOTHERDUCK_TOKEN` | SQL resources and catalog reads |
| `MOTHERDUCK_ADMIN_TOKEN` | REST organization administration and embed sessions |

Supply only the credential needed by the configuration. Keep credentials out of
committed `.tf` and `.tfvars` files. Terraform's `sensitive` flag masks output;
it does not encrypt state or saved plans. Use an encrypted backend with locking
and restricted access, and commit the root module's `.terraform.lock.hcl`.

Configure providers in the root module and pass them to child modules.
Provider-level `database` must already exist; use resource-level database
references when creating a database in the same apply.

## Capabilities and constraints

- SQL infrastructure and application resources use public MotherDuck SQL.
  Function availability depends on the account, region, permissions, and client.
- REST administration uses the public MotherDuck API and a separate admin token.
- Catalog data sources expose metadata; some values, including share URLs and
  Flight output, are sensitive.
- Prefer the [ephemeral Dive embed session](docs/ephemeral-resources/dive_embed_session.md)
  on Terraform 1.10 or later. The legacy data source persists its credential.
- Flight definitions are durable configuration. Creating or replacing a
  [Flight run](docs/resources/flight_run.md) can execute Python again.
- General Terraform support starts at 1.5. The tested Terraform/OpenTofu versions
  are listed in [CI and release](docs/guides/ci-and-release.md).
- CGO packages target Linux and macOS on amd64 and arm64. Windows is not currently
  a supported release target.

For multi-tenant configurations, start with [writer bootstrap](docs/blueprints/writer-bootstrap.md),
[hypertenancy](docs/blueprints/hypertenancy.md), or
[read hypertenancy](docs/blueprints/read-hypertenancy.md).
Review their ownership and credential requirements before applying them.

## Working in this repository: agents and contributors

Read [AGENTS.md](AGENTS.md) and [CONTRIBUTING.md](CONTRIBUTING.md), then the guide
matching your task above. Inspect current code and Git state before editing;
use existing conventions and preserve unrelated changes.

| Source of truth | Location |
| --- | --- |
| Registered public surfaces | [Catalog manifest](internal/motherduck/catalog.yaml) and provider registration |
| Resource state, imports, and CRUD | [Resources](internal/resources) |
| Data-source schemas and reads | [Data sources](internal/datasources) |
| Authentication, connections, and request behavior | [Provider configuration](internal/provider/provider.go), [clients](internal/client) |
| Generated documentation inputs | [Schemas](internal), [templates](templates), [examples](examples) |
| Test commands and hosted gates | [Makefile](Makefile), [workflows](.github/workflows) |

Edit schemas, templates, or examples before regenerating `docs/`.
Do not hand-edit generated pages. Internal Go packages are implementation details,
not a supported public library.

Run commands from the repository root:

```shell
make test-unit       # Hermetic Go tests, race detection, randomized order
make test-contract   # Terraform lifecycle against strict local backends
make test-cli        # Examples and diagnostics using one local provider build
make docs            # Regenerate reference after schema/template/example changes
make pre-push-check  # Required before opening or updating a PR
make release-check   # Required for release scripts/workflows/packaging changes
```

For SQL changes, run `make test-live-required` with `MOTHERDUCK_TOKEN` supplied
through managed injection. It creates test infrastructure and audits cleanup.
Select additional live tests using the testing guide; live operations can incur
costs. A skipped feature or a filter matching zero tests is not successful
coverage. Never include token-backed logs or complete state in public PRs.

Preserve public schemas and import formats unless a breaking change is intended
and documented. Verify observable state and remote effects, including no-op
refresh and cleanup, rather than adding tests that merely mirror the code.

Report the exact commit and checks completed, and distinguish local validation,
PR status, merge, and publication. Publish only through the documented release
workflow when release authorization and prerequisites are satisfied.

## Security and license

Report vulnerabilities privately using [SECURITY.md](SECURITY.md).
Licensed under [Apache 2.0](LICENSE).
