---
page_title: "Run a local provider build"
subcategory: "Contributing"
description: |-
  Build the provider from source and run it with Terraform development overrides.
---

# Run a local provider build

You need the Go toolchain named in `go.mod`, a C/C++ compiler for CGO, Terraform,
and the shell tools the Makefile uses. Builds run on Linux or macOS, amd64 or
arm64. `make release-check` also needs `zip`, `unzip`, and `jq`. Tests also need
ShellCheck and `gpg`.

From the repository root:

```shell
mkdir -p bin
go build -o bin/terraform-provider-motherduck .
```

Create a Terraform CLI configuration file with the absolute path to `bin`:

```hcl
provider_installation {
  dev_overrides {
    "registry.terraform.io/motherduckdb/motherduck" = "/absolute/path/to/repository/bin"
  }
  direct {}
}
```

Set `TF_CLI_CONFIG_FILE` to that file in your development shell. Terraform prints
a development-override warning, which is expected. In a module that uses only
this provider, skip `terraform init` and run `terraform validate`, `plan`, and
`apply` directly.

The test scripts use filesystem mirrors instead of development overrides so they
can exercise `terraform init`.

## Which checks to run

- Schema or validator changes: unit tests, invalid-configuration fixtures, examples, and `make docs`.
- Resource behavior: a protocol contract, plus a live smoke where real service behavior matters.
- SQL client changes: embedded DuckDB tests and `make test-live-required`.
- Packaging changes: `make release-check`.

Run `make pre-push-check` before pushing. See [Test the provider](testing.md)
for all targets.
