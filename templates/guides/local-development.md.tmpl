---
page_title: "Run a local provider build"
subcategory: "Contributing"
description: |-
  Build the provider from source and run it with Terraform development overrides.
---

# Run a local provider build

Install Go using the toolchain in `go.mod`, a native C/C++ compiler for CGO,
Terraform, and the shell utilities used by the Makefile. Native package checks
also require `zip`, `unzip`, and `jq`. Use the supported Linux/macOS architecture
matching the build host.

From the repository root:

```shell
mkdir -p bin
go build -o bin/terraform-provider-motherduck .
```

Create a separate Terraform CLI configuration file using the absolute path to
that `bin` directory:

```hcl
provider_installation {
  dev_overrides {
    "registry.terraform.io/motherduckdb/motherduck" = "/absolute/path/to/repository/bin"
  }
  direct {}
}
```

Set `TF_CLI_CONFIG_FILE` to that file for your development shell. Terraform
prints a development-override warning. This is expected. In a module containing
only this provider, skip `terraform init` and run `terraform validate`, `plan`,
and `apply` directly. Initialize other providers and remote modules separately
when needed.

The tests use filesystem mirrors instead of development overrides so they can
exercise `terraform init` without requiring Registry publication.

## Choose the relevant check

- Schema or validator changes: unit tests, invalid-configuration fixtures,
  generated docs, and examples.
- Resource behavior: a Terraform protocol contract plus the corresponding
  live smoke where real service behavior matters.
- SQL client changes: embedded DuckDB tests and `make test-live-required`.
- Packaging changes: `make release-check` and the four native CI package jobs.

Run `make pre-push-check` before pushing. See [testing](testing.md) for the
coverage map and credential boundaries.
