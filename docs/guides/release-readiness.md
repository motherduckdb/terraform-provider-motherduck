---
page_title: "Prepare a release"
subcategory: "Contributing"
description: |-
  The checklist to complete before and after tagging a provider release.
---

# Prepare a release

Releases are tag-driven. The [CI and release guide](ci-and-release.md) describes
what the release workflow runs and which assets it publishes.

## Before tagging

1. Write `release-notes/<tag>.md` for users, with public change links. Add an
   "Upgrading" section when the release changes behavior or needs a new version
   constraint. Do not add a "Verification" section. Put test evidence in the
   release pull request. The workflow fails without this file.
2. Run `make pre-push-check`, `make release-check`, and `make release-sign-check`.
3. Merge the release PR after all required checks pass.
4. Confirm CI and `Live exact-main` passed on the merged commit.
5. Tag that `main` commit and push the tag. The workflow rejects tags on commits
   that are not in `main`.

```bash
git tag vX.Y.Z
git push origin vX.Y.Z
```

## After tagging

1. Confirm the release workflow succeeded and the release has four platform
   ZIPs, `SHA256SUMS`, `SHA256SUMS.sig`, and `manifest.json`.
2. Download one ZIP into a fresh directory, verify its checksum and provenance,
   and install it as described in [Install from GitHub Releases](github-installation.md).
3. After the Terraform Registry lists the new version, open a follow-up PR that:
   - Replaces the previous tag in pinned links and module sources under
     `templates/` and `examples/`. Find them with
     `grep -rnE '(tree|blob)/v[0-9]+\.[0-9]+\.[0-9]+|\?ref=v[0-9]|--branch v[0-9]' templates examples`.
     They use the forms `tree/vX.Y.Z`, `blob/vX.Y.Z`, `?ref=vX.Y.Z`, and
     `--branch vX.Y.Z`.
   - Updates the provider version in `examples/pulumi/python/Pulumi.yaml` and
     `examples/pulumi/python/README.md`. The Pulumi bridge installs that version
     from the Registry.
   - Runs `make docs` and `make test-pulumi`.

Never overwrite published assets or reuse a version. Dependency lock files pin
package digests, so fix a bad release by publishing a new version.

Registry ingestion needs the detached GPG signature. GitHub provenance does not
replace it, and a release built without the signing secrets cannot be ingested.
See the Registry's
[publishing requirements](https://developer.hashicorp.com/terraform/registry/providers/publishing).
