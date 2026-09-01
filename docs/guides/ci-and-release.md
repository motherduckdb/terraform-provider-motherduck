# CI and Release

This repository uses separate workflows for pull-request checks, live MotherDuck smoke tests, and tag-driven releases.

## Pull Request And Push CI

`.github/workflows/ci.yml` runs on pull requests, pushes to `main`, and manual dispatch.

The required offline jobs run independently:

```bash
make static-check
make release-check
make test-unit test-contract
```

`Static checks` owns formatting, linting, vulnerability, workflow, shell, docs, examples, packaging, repository hygiene, and provider builds. `Behavior contracts` owns race-enabled unit tests and Terraform protocol lifecycles against hermetic SQL and REST clients. A global coverage percentage is not required; each contract must assert externally visible state and exact backend side effects.

The Terraform compatibility job repeats the offline Terraform checks against supported Terraform versions:

- `1.5.7`
- `1.8.5`
- `1.12.2`
- `1.15.8`
- `1.15.9`

These checks validate examples, invalid-configuration diagnostics, and missing-credential diagnostics without making live MotherDuck calls.

The live-smoke workflow also runs the provider against OpenTofu. The default OpenTofu version is `1.12.6`; override it with the `opentofu_versions` manual workflow input or `TOFU_VERSIONS` locally.

## Live Smoke

`.github/workflows/live-smoke.yml` runs a trusted live contract on every push to `main`, runs the compatibility matrix weekly, and can be started manually. Pull-request code never receives live credentials.

The protected `motherduck-live` GitHub environment supplies:

- `MOTHERDUCK_TOKEN`: read-write MotherDuck token for SQL-backed resources and data sources.
- `MOTHERDUCK_ADMIN_TOKEN`: organization-admin MotherDuck token for REST administration resources.

Missing live credentials fail the job instead of producing a successful skip. The exact-`main` job uses Terraform `1.15.9` and runs `make test-live-required` for SQL, REST, import, no-op-plan, destroy, and cleanup behavior.

The weekly matrix runs read-only checks on every supported Terraform version and OpenTofu `1.12.6`. SQL lifecycle checks run on Terraform `1.5.7`, Terraform `1.15.9`, and OpenTofu `1.12.6`; the blueprint lifecycle runs on Terraform `1.15.9`. Manual inputs can request lifecycle coverage for every selected version.

## Releases

`.github/workflows/release.yml` runs when a semantic version tag is pushed:

```bash
git tag v0.1.0
git push origin v0.1.0
```

The release workflow:

1. Runs the full preflight gate and release packaging check.
2. Runs `make test-live-required` on the exact tagged commit using the protected live environment.
3. Builds native provider packages.
4. Creates a SHA256 digest in each platform build job.
5. Downloads all platform packages and per-build digests into one release job.
6. Verifies the per-build digests.
7. Creates registry SHA256 checksums.
8. Signs the checksum file with the configured GPG key.
9. Adds the Terraform Registry manifest.
10. Publishes GitHub build-provenance attestations for package and release artifacts.
11. Creates the GitHub release for the tag.

Terraform Registry publishing expects versioned GitHub releases with provider zip files, checksums, and a detached signature. The repository owner must also register the provider and GPG public key in the Terraform Registry before users can install `source = "motherduckdb/motherduck"` from the registry.

Release secrets:

- `GPG_PRIVATE_KEY`: private key used only by the release workflow to sign checksum files.
- `GPG_PASSPHRASE`: passphrase for `GPG_PRIVATE_KEY`.

Initial release targets:

- `linux_amd64`
- `linux_arm64`
- `darwin_amd64`
- `darwin_arm64`

The provider embeds DuckDB through CGO, so release packages are built on native operating system runners instead of cross-compiled with a generic release tool. `scripts/package-release.sh` is a deliberate small packaging path for those native builds: it builds the provider binary with CGO enabled, names the binary according to Terraform Registry conventions, and zips one platform package per runner.

Add a target only after proving its native runner can build `scripts/package-release.sh` and Terraform can initialize the produced provider binary. Windows packages are intentionally not published until there is a tested native Windows CGO build path.

## Local Release Checks

Run the release package check before pushing release workflow changes:

```bash
make release-check
```

Create a local package under `dist/`:

```bash
VERSION=0.1.0 make release-package-local
```

Local packages use this naming convention:

```text
terraform-provider-motherduck_<version>_<os>_<arch>.zip
```
