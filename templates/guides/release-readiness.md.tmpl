# Prepare a GitHub release

Current distribution is GitHub Releases only. The provider is not published in
the Terraform Registry. Users install through the [filesystem mirror guide](github-installation.md).

## Validate the candidate

1. Prepare version-specific release notes with public change and verification links.
2. Run `make pre-push-check` and `make release-check`.
3. Merge a reviewed PR after all required checks pass.
4. Verify CI and live SQL tests on the exact merged commit.
5. Confirm the protected `motherduck-live` environment supplies the SQL test token.
6. Tag that commit with a semantic version and push the tag.

The tag workflow runs preflight, live tests, and native installation tests, then
publishes four ZIPs, checksums, a protocol manifest, and GitHub build-provenance
attestations. It does not require GPG credentials or publish to the Registry.

## Verify publication

Check that the tag matches the tested commit, the release workflow succeeds,
and all four platform ZIPs are present. Download a ZIP and checksums into a fresh
directory, verify its digest and provenance, then install it with the documented
filesystem mirror and run Terraform schema discovery and validation.

Do not overwrite published artifacts or reuse a version. Correct a release by
publishing a new version because dependency lock files depend on immutable digests.

## Future Registry publication

Registry distribution is a separate decision. Before enabling it, configure
publisher access and GPG signing, follow
[HashiCorp's publishing requirements](https://developer.hashicorp.com/terraform/registry/providers/publishing),
and test installation from the Registry. GitHub provenance does not substitute
for the Registry's detached GPG signature requirement.
