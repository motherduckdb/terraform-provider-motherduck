---
page_title: "Install from GitHub Releases"
subcategory: "Operations"
description: |-
  Install signed provider packages from GitHub Releases through a filesystem mirror.
---

# Install from GitHub Releases

Most users do not need this guide. The provider is published as
[motherduckdb/motherduck](https://registry.terraform.io/providers/motherduckdb/motherduck/latest),
so `terraform init` installs it and verifies the publisher signature.

Install directly from
[GitHub Releases](https://github.com/motherduckdb/terraform-provider-motherduck/releases)
when you work in an air-gapped environment, want to pin a locally verified
artifact, or need v0.2.1 or earlier, which are not on the Registry.

## Download and verify

You need Terraform 1.5 or later. Pick your platform: `linux_amd64`,
`linux_arm64`, `darwin_amd64` (Intel Mac), or `darwin_arm64` (Apple Silicon).
Each release has one ZIP per platform plus these files:

- `terraform-provider-motherduck_<version>_SHA256SUMS`
- `terraform-provider-motherduck_<version>_SHA256SUMS.sig`
- `terraform-provider-motherduck_<version>_manifest.json`

The example uses the GitHub CLI, `shasum` (use `sha256sum` on Linux), and `awk`.
You can also download the files from the release page.

```shell
version=0.3.8
platform=darwin_arm64
archive="terraform-provider-motherduck_${version}_${platform}.zip"
sums="terraform-provider-motherduck_${version}_SHA256SUMS"
download_dir="$(mktemp -d)"
gh release download "v${version}" \
  --repo motherduckdb/terraform-provider-motherduck \
  --pattern "${archive}" --pattern "${sums}" --pattern "${sums}.sig" \
  --dir "${download_dir}"
expected="$(awk -v name="${archive}" '$2 == name { print $1 }' "${download_dir}/${sums}")"
actual="$(shasum -a 256 "${download_dir}/${archive}" | awk '{print $1}')"
test -n "${expected}" && test "${actual}" = "${expected}"
```

Continue only if the last command succeeds. A checksum detects corruption, not
substitution. To check the publisher, import the public key registered on the
Terraform Registry and verify the signature over the checksum file:

```shell
gpg --verify "${download_dir}/${sums}.sig" "${download_dir}/${sums}"
```

Releases also carry GitHub build-provenance attestations:

```shell
gh attestation verify "${download_dir}/${archive}" \
  --repo motherduckdb/terraform-provider-motherduck
```

## Configure a mirror

Copy the ZIP into a persistent mirror directory:

```shell
mirror="${PWD}/provider-mirror"
mkdir -p "${mirror}/registry.terraform.io/motherduckdb/motherduck"
cp "${download_dir}/${archive}" "${mirror}/registry.terraform.io/motherduckdb/motherduck/"
```

Create a Terraform CLI configuration file, for example `provider-installation.tfrc`,
with the absolute path of `mirror`:

```hcl
provider_installation {
  filesystem_mirror {
    path    = "/absolute/path/to/provider-mirror"
    include = ["registry.terraform.io/motherduckdb/motherduck"]
  }
  direct {
    exclude = ["registry.terraform.io/motherduckdb/motherduck"]
  }
}
```

Set `TF_CLI_CONFIG_FILE` to that file's absolute path in the shell or CI job that
runs Terraform. This replaces your usual CLI configuration, so add any other
installation settings you need. Do not commit downloaded binaries or
machine-specific paths.

In your root module, pin the same version:

```hcl
terraform {
  required_providers {
    motherduck = {
      source  = "motherduckdb/motherduck"
      version = "= 0.3.8"
    }
  }
}

provider "motherduck" {}
```

Run `terraform init`, `terraform providers schema -json`, and `terraform validate`.
Terraform may report the mirror package as unauthenticated because it has no
Registry signing chain. It still installs. Commit the resulting dependency lock
file. Credentials are needed only for plan and apply.

In CI, make the mirror and CLI configuration available on each runner before
`terraform init`, with the ZIP for that runner's platform.
