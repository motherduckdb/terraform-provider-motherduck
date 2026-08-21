# CI and Release

This repository uses separate workflows for pull-request checks, live MotherDuck smoke tests, and tag-driven releases.

## Pull Request And Push CI

`.github/workflows/ci.yml` runs on pull requests, pushes to `main`, and manual dispatch.

The static job runs:

```bash
make pre-push-check
make release-check
```

The Terraform compatibility job repeats the offline Terraform checks against supported Terraform versions:

- `1.5.7`
- `1.8.5`
- `1.12.2`
- `1.15.9`

These checks validate examples, invalid-configuration diagnostics, and missing-credential diagnostics without making live MotherDuck calls.

The live-smoke workflow also runs the provider against OpenTofu. The default OpenTofu version is `1.12.6`; override it with the `opentofu_versions` manual workflow input or `TOFU_VERSIONS` locally.

## Live Smoke

`.github/workflows/live-smoke.yml` runs weekly and can be started manually.

Repository secrets:

- `MOTHERDUCK_TOKEN`: read-write MotherDuck token for SQL-backed resources and data sources.
- `MOTHERDUCK_ADMIN_TOKEN`: organization-admin MotherDuck token for REST administration resources. If omitted, SQL checks can still run and REST lifecycle checks skip.

Manual inputs choose the default read-only Terraform/OpenTofu version matrix, a SQL lifecycle matrix, or the SQL-only blueprint lifecycle matrix. The workflow expands Terraform and OpenTofu versions into separate matrix jobs, then runs one cleanup audit after the matrix finishes.

## Releases

`.github/workflows/release.yml` runs when a semantic version tag is pushed:

```bash
git tag v0.1.0
git push origin v0.1.0
```

The release workflow:

1. Runs the full preflight gate and release packaging check.
2. Builds native provider packages.
3. Creates a SHA256 digest in each platform build job.
4. Downloads all platform packages and per-build digests into one release job.
5. Verifies the per-build digests.
6. Creates registry SHA256 checksums.
7. Signs the checksum file with the configured GPG key.
8. Adds the Terraform Registry manifest.
9. Publishes GitHub build-provenance attestations for package and release artifacts.
10. Creates the GitHub release for the tag.

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
