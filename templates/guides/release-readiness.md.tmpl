# Prepare an official provider release

This checklist separates repository readiness from publication. Follow
HashiCorp's [publishing requirements](https://developer.hashicorp.com/terraform/registry/providers/publishing).

## Before the first tag

1. Confirm the public repository name is `terraform-provider-motherduck` and
   the intended Registry namespace is `motherduckdb`.
2. Register the provider and GPG public key with the Terraform Registry.
   Confirm organization ownership and any desired partner status separately.
3. Configure `GPG_PRIVATE_KEY` and `GPG_PASSPHRASE` as release secrets.
   Never put private keys in source or test artifacts.
4. Configure `MOTHERDUCK_TOKEN` in the protected `motherduck-live` environment,
   limited to trusted main and release tags.
5. Verify historical release notes against public commits and PRs. Notes from
   earlier repository history are not evidence of published versions. Select the
   initial version from actual Registry/tag history, not fixture version numbers.
6. Preview every generated surface in the
   [Registry documentation preview](https://developer.hashicorp.com/terraform/registry/providers/docs).

## Verify a candidate

Require static checks, behavior contracts, supported Terraform lanes, and all
four native package jobs before merging. Repository branch protection must name
the package jobs; YAML alone does not make them required.

Wait for live database/schema/table/view checks on the exact merged commit. Run focused smokes for changed
surfaces and the stable SQL suite. Before the initial official release, also
validate service-account, token, and Duckling lifecycles with an authorized
admin token. Hermetic HTTP tests do not prove deployed API compatibility or
account permissions. Record the commit, CLI versions, checks, and cleanup
results; keep sensitive logs private.

Prepare accurate `release-notes/vX.Y.Z.md` with public evidence links. Tag the
validated commit using semantic versioning, then push the tag. The release
workflow checks live SQL and installs native packages before publication.

## Verify publication

Expect four platform ZIPs, a versioned manifest declaring protocol `6.0`,
SHA-256 checksums covering ZIPs and manifest, and a binary detached GPG signature.
Check the signature against the Registry public key and install the published
version with `terraform init` in a fresh directory.

Do not overwrite published artifacts or reuse a version: lock files depend on
immutable checksums. Publish a new version to correct a published release.

A green CI run does not prove Registry registration, signing-key ownership,
partner verification, or live behavior outside the test cases that ran.
