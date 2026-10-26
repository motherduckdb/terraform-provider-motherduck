---
page_title: "CI and release"
subcategory: "Contributing"
description: |-
  How pull-request, live, and tag-driven release workflows gate this provider.
---

# CI and release

Three workflows gate the provider:

| Workflow | Runs on | Credentials |
| --- | --- | --- |
| `.github/workflows/ci.yml` | Pull requests, pushes to `main`, manual dispatch | None |
| `.github/workflows/live-smoke.yml` | Pushes to `main`, weekly schedule, manual dispatch | Protected `motherduck-live` environment |
| `.github/workflows/release.yml` | Semantic version tags | `motherduck-live` and `motherduck-release` environments |

Pull-request code never receives live or signing credentials.

## Pull request CI

`ci.yml` runs these jobs independently:

| Job | Command | Covers |
| --- | --- | --- |
| `Static checks` | `make ci-static-check` | Formatting, lint, vulnerability scan, workflow and shell checks, script unit tests, docs drift, repository hygiene, build |
| `Behavior contracts` | `make test-unit test-contract` | Race-enabled unit tests and Terraform protocol lifecycles against hermetic SQL and REST clients |
| `Pulumi bridge` | `make test-pulumi` | Pulumi schema generation and the Python example preview |
| `Terraform <version>`, `OpenTofu <version>` | `make test-cli-versions` | Example validation, import, invalid-configuration, and missing-credential diagnostics |
| `Package <runner>` | `make release-check` | Release ZIP build, filesystem mirror install, schema discovery, validation |

`make ci-static-check` is `make static-check` without `test-cli`, because the CLI
matrix already runs that suite. The local `make pre-push-check` runs both.

The CLI matrix covers these versions. Each CLI download is checked against the
HashiCorp or OpenTofu release signing key.

- Terraform `1.5.7`, `1.8.5`, `1.12.2`, `1.15.8`, `1.15.9`, `1.16.1`, `1.16.4`
- OpenTofu `1.12.6`

Package jobs run on `ubuntu-22.04`, `ubuntu-22.04-arm`, `macos-15-intel`, and
`macos-15`, the same runners the release workflow builds on.

The `Pulumi bridge` job installs the Pulumi CLI pinned in
`scripts/lib/pulumi-cli.sh` and generates the schema through the pinned Any
Terraform Provider bridge. It fails when a resource or data source does not map,
or when the [Pulumi guide](pulumi.md) does not name an ephemeral resource the
bridge cannot run. Bump the CLI, bridge, and example pins together.

## Live checks

The `motherduck-live` environment supplies:

- `MOTHERDUCK_TOKEN`: read-write token for SQL-backed resources and data sources.
- `MOTHERDUCK_ADMIN_TOKEN`: optional organization-admin token. Admin jobs fall back to `MOTHERDUCK_TOKEN` when it is not set.

Use a dedicated test organization. Keep the environment's deployment rules
restricted to `main` and release tags. After rotating a token, run the live
workflow on `main` to verify it. A missing `MOTHERDUCK_TOKEN` fails the job.

On every push to `main`, `live-smoke.yml` runs:

- `Live exact-main`: `make test-live-required` on Terraform `1.16.4`.
- `Live admin lifecycle`: `make test-live-admin`. It creates disposable service accounts, rotates tokens, repairs revoked role grants, and checks writer/reader isolation.
- `Live Pulumi lifecycle`: `make test-live-pulumi`, after the other two finish. It drives `test-fixtures/pulumi-live` through create, refresh, update, replacement, import, and destroy, then audits cleanup.

The weekly schedule and manual runs use `Live MotherDuck smoke`, which runs
`make test-terraform-versions` once per CLI version, followed by
`Audit live cleanup`. The schedule runs read-only checks on every version, SQL
lifecycle checks on Terraform `1.5.7`, Terraform `1.16.4`, and OpenTofu `1.12.6`,
and the blueprint lifecycle on Terraform `1.16.4`. Manual inputs:

| Input | Effect |
| --- | --- |
| `terraform_versions`, `opentofu_versions` | Override the version lists |
| `run_sql_lifecycle`, `run_blueprint_lifecycle` | Run those lifecycles on every selected version |
| `run_admin_lifecycle` | Run the admin and Pulumi jobs on `main` |

Preview-surface smokes (Guide, Dive, Flight, Dive-and-Flight blueprint) exit
successfully with a skip when the account lacks the required `MD_*` functions.
A green matrix is therefore not proof of coverage. Set the matching
`MD_TF_ACC_REQUIRE_*` variable to fail instead.

Live Smoke and the release `Live release contracts` job share the
`motherduck-live-account` concurrency group, so one run uses the account at a
time. GitHub keeps one pending run per group. A pending release job can be
superseded by Live Smoke and must then be re-run.

## Release workflow

`release.yml` runs on tags matching `vX.Y.Z` or `vX.Y.Z-*`. Tags with a suffix
are published as prereleases. Follow the [release checklist](release-readiness.md).

1. `Preflight` requires `release-notes/<tag>.md` and a tag on a commit already
   in `main`. It runs `make release-preflight-check release-check`. The
   preflight gate is the pull-request gate without `vulncheck`, which runs as an
   advisory step so a new advisory in an indirect dependency cannot block an
   unrelated release.
2. `Live release contracts` runs `make test-live-required` on the tagged commit.
3. `Build <os>_<arch>` packages the provider on each native runner with
   `scripts/package-release.sh`, installs and starts it with
   `scripts/test-release-package.sh`, records a digest, and attests provenance.
4. `Publish GitHub release` verifies the digests, adds the Registry manifest,
   writes `SHA256SUMS`, signs it with `scripts/sign-release.sh`, attests all
   assets, and creates the release.

Signing uses `GPG_PRIVATE_KEY` and `GPG_PASSPHRASE` secrets and the
`GPG_FINGERPRINT` variable on the protected `motherduck-release` environment.

Release assets for version `X.Y.Z`:

```text
terraform-provider-motherduck_X.Y.Z_linux_amd64.zip
terraform-provider-motherduck_X.Y.Z_linux_arm64.zip
terraform-provider-motherduck_X.Y.Z_darwin_amd64.zip
terraform-provider-motherduck_X.Y.Z_darwin_arm64.zip
terraform-provider-motherduck_X.Y.Z_manifest.json
terraform-provider-motherduck_X.Y.Z_SHA256SUMS
terraform-provider-motherduck_X.Y.Z_SHA256SUMS.sig
```

## Native builds

The provider embeds DuckDB through CGO, so each package is built on a native
runner instead of cross-compiled. Linux packages are built on Ubuntu 22.04 with
a static C++ runtime and require at most glibc 2.34. macOS packages target
macOS 13. `scripts/test-release-package.sh` fails when a package raises either
floor.

Add a target only after its native runner can build `scripts/package-release.sh`
and Terraform can initialize the result. Windows is not published until there is
a tested native Windows CGO build.

## Local release checks

Run these before pushing changes to release scripts or workflows:

```bash
make release-check
make release-sign-check
```

`make release-sign-check` needs `gpg` and signs with a throwaway key. It also
runs inside `make test-scripts`.

Build a package for the current platform under `dist/`:

```bash
VERSION=0.1.0 make release-package-local
```
