---
page_title: "CI and Release"
subcategory: "Contributing"
---

# CI and Release

This repository uses separate workflows for pull-request checks, live MotherDuck smoke tests, and tag-driven releases.

## Pull Request And Push CI

`.github/workflows/ci.yml` runs on pull requests, pushes to `main`, and manual dispatch.

The required offline jobs run independently:

```bash
make ci-static-check
make release-check
make test-unit test-contract
```

`Static checks` owns formatting, linting, vulnerability, workflow, shell, docs,
repository hygiene, and provider builds. The CLI matrix owns example validation
and diagnostic checks, so the static job does not run that suite a second time.
Native package jobs own packaging and installation checks. `Behavior contracts`
owns race-enabled unit tests and Terraform protocol lifecycles against hermetic
SQL and REST clients. A global coverage percentage is not required. Each contract
must assert externally visible state and exact backend side effects.

Go's module and build caches are managed by `setup-go`. Static and release
preflight jobs additionally cache lint and documentation tools by operating
system, architecture, Go version, Makefile, and dependency lockfile. Tools are
still checked against their pinned versions. Provider binaries, Terraform state,
and test results are not reused from this tool cache. The local
`make pre-push-check` continues to run the full static and CLI suites.

The Terraform compatibility job repeats the offline Terraform checks against supported Terraform versions:

- `1.5.7`
- `1.8.5`
- `1.12.2`
- `1.15.8`
- `1.15.9`
- `1.16.1`

Every matrix job runs `make test-cli-versions` with a checksum-verified CLI
download. OpenTofu `1.12.6` is included alongside Terraform. Each job checks examples, invalid imports, invalid-configuration diagnostics, and missing-credential diagnostics against one freshly built local provider, without making live MotherDuck calls.

The live-smoke workflow also runs the provider against OpenTofu. The default OpenTofu version is `1.12.6`. Override it with the `opentofu_versions` manual workflow input or `TOFU_VERSIONS` locally.

## Native package checks

Four native package jobs run on Linux amd64/arm64 and macOS Intel/ARM runners.
Each builds the release ZIP, installs it through a filesystem mirror, starts the
plugin through Terraform schema discovery, and validates its configuration.
Static and CLI jobs have bounded runtimes. Behavior tests run with race detection.

## Trusted live checks

`.github/workflows/live-smoke.yml` runs a trusted live contract on every push to `main`, runs the compatibility matrix weekly, and can be started manually. Pull-request code never receives live credentials.

The protected `motherduck-live` GitHub environment supplies:

- `MOTHERDUCK_TOKEN`: read-write MotherDuck token for SQL-backed resources and data sources.
- `MOTHERDUCK_ADMIN_TOKEN`: organization-admin token for the isolated admin lifecycle job.

Use credentials from a dedicated test organization. Store both values as secrets
in the protected `motherduck-live` environment, never in repository files.
Rotate them from the approved secret-manager entry and run the live workflow on
`main` to verify the replacement. Environment deployment rules must remain
restricted to `main` and release tags.

Missing `MOTHERDUCK_TOKEN` fails the job instead of producing a successful skip. The exact-`main` job uses Terraform `1.16.1` and runs `make test-live-required` for SQL, import, no-op-plan, destroy, and cleanup behavior. REST administration behavior is gated hermetically in pull requests. The separate admin job receives an admin token only on `main` pushes or an
explicit manual `run_admin_lifecycle` request on `main`. It creates disposable
accounts, rotates tokens, repairs revoked role grants, verifies writer/reader
isolation, and checks cleanup. Pull-request code never receives either token.

The weekly matrix runs read-only checks on every supported Terraform version and OpenTofu `1.12.6`. SQL lifecycle checks run on Terraform `1.5.7`, Terraform `1.16.1`, and OpenTofu `1.12.6`. The blueprint lifecycle runs on Terraform `1.16.1`. Manual inputs can request lifecycle coverage for every selected version.

The SQL lifecycle matrix runs the database drop-with-objects smoke plus the Guide, Dive, Flight, Dive-and-Flight blueprint, and share-grant drift smokes. Each preview-surface smoke exits successfully with a skip when the account does not expose the required `MD_*` functions, so a green matrix is not proof of coverage. Set the matching `MD_TF_ACC_REQUIRE_*` variable to turn a skip into a failure.

## Releases

Releases are published to GitHub Releases with signed checksums. Follow the
[release checklist](release-readiness.md) and the
[installation guide](github-installation.md).

`.github/workflows/release.yml` runs when a semantic version tag is pushed:

```bash
git tag v0.1.0
git push origin v0.1.0
```

The release workflow:

1. Runs the release preflight gate (`make release-preflight-check`) and release packaging check. The preflight is the pull-request gate without `vulncheck`. Vulnerability scanning still runs in the release job as an advisory step so a new advisory in an indirect dependency cannot block an unrelated release. Pull-request CI keeps `vulncheck` blocking.
2. Runs the required SQL lifecycle on the exact tagged commit using the protected live environment.
3. Builds native provider packages.
4. Creates a SHA256 digest in each platform build job.
5. Downloads all platform packages and per-build digests into one release job.
6. Verifies the per-build digests.
7. Creates registry SHA256 checksums.
8. Adds the Terraform Registry manifest.
9. Signs the checksum file with the publisher GPG key from the protected
   `motherduck-release` environment, producing the detached binary
   `SHA256SUMS.sig` the Registry requires.
10. Publishes GitHub build-provenance attestations for package and release artifacts.
11. Verifies the checksum, signature, and manifest are present, then creates the
    GitHub release for the tag.

Signing is the only release step that needs credentials beyond the live test
token. The publisher key and its passphrase are held as secrets on the protected
`motherduck-release` environment. `GPG_FINGERPRINT` pins the expected key.

Initial release targets:

- `linux_amd64`
- `linux_arm64`
- `darwin_amd64`
- `darwin_arm64`

The provider embeds DuckDB through CGO, so release packages are built on native operating system runners instead of cross-compiled with a generic release tool. `scripts/package-release.sh` is a deliberate small packaging path for those native builds: it builds the provider binary with CGO enabled, names the binary according to Terraform Registry conventions, and zips one platform package per runner.

Add a target only after proving its native runner can build `scripts/package-release.sh` and Terraform can initialize the produced provider binary. Windows packages are intentionally not published until there is a tested native Windows CGO build path.

## Local Release Checks

Run the release package and signing checks before pushing release workflow changes:

```bash
make release-check
make release-sign-check
```

`make release-sign-check` needs `gpg` and uses a throwaway key, so it never
touches the publisher key. It also runs inside `make static-check`.

Create a local package under `dist/`:

```bash
VERSION=0.1.0 make release-package-local
```

Local packages use this naming convention:

```text
terraform-provider-motherduck_<version>_<os>_<arch>.zip
```
