---
page_title: "Prepare a release"
subcategory: "Contributing"
---

# Prepare a release

Releases go to GitHub Releases with signed checksums. Users install through the
[filesystem mirror guide](github-installation.md) until the provider is listed in
the Terraform Registry.

## Validate the candidate

1. Prepare version-specific release notes with public change and verification links.
2. Run `make pre-push-check`, `make release-check`, and `make release-sign-check`.
3. Merge a reviewed PR after all required checks pass.
4. Verify CI and live SQL tests on the exact merged commit.
5. Confirm the protected `motherduck-live` environment supplies the SQL test token.
6. Tag that commit with a semantic version and push the tag.

The tag workflow runs preflight, live tests, and native installation tests, then
publishes four ZIPs, checksums, a detached GPG signature over the checksums, a
protocol manifest, and GitHub build-provenance attestations. Signing reads the
publisher key from the protected `motherduck-release` environment.

## Verify publication

Check that the tag matches the tested commit, the release workflow succeeds, and
all four platform ZIPs plus `SHA256SUMS`, `SHA256SUMS.sig`, and `manifest.json`
are present. Download a ZIP and checksums into a fresh directory, verify its
digest and provenance, then install it with the documented filesystem mirror and
run Terraform schema discovery and validation.

Do not overwrite published artifacts or reuse a version. Correct a release by
publishing a new version because dependency lock files depend on immutable digests.

## Registry publication

Release artifacts satisfy the Terraform Registry's
[publishing requirements](https://developer.hashicorp.com/terraform/registry/providers/publishing),
including the detached GPG signature that GitHub provenance does not substitute
for. A release built without the signing secrets cannot be ingested.
