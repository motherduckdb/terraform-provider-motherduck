---
page_title: "Install from GitHub Releases"
subcategory: "Operations"
---

# Install from GitHub Releases

Most users do not need this guide. The provider is published as
[motherduckdb/motherduck](https://registry.terraform.io/providers/motherduckdb/motherduck/latest),
so `terraform init` installs it and verifies the publisher signature with no
extra configuration.

Use this guide to install a release directly from
[GitHub Releases](https://github.com/motherduckdb/terraform-provider-motherduck/releases)
instead: in an air-gapped environment, to pin a locally verified artifact, or
for a version predating Registry publication (v0.2.1 and earlier are not on the
Registry). The source address identifies the provider; a filesystem mirror tells
Terraform where to obtain it.

## Download and verify

Use Terraform 1.5 or later and select your platform: `linux_amd64`,
`linux_arm64`, `darwin_amd64` (Intel Mac), or `darwin_arm64` (Apple Silicon).
The example uses the GitHub CLI, `shasum`, and `awk`. Alternatively download
the same files from the release page.

```shell
version=0.1.0
platform=darwin_arm64
archive="terraform-provider-motherduck_${version}_${platform}.zip"
sums="terraform-provider-motherduck_${version}_SHA256SUMS"
download_dir="$(mktemp -d)"
gh release download "v${version}" \
  --repo motherduckdb/terraform-provider-motherduck \
  --pattern "${archive}" --pattern "${sums}" --dir "${download_dir}"
expected="$(awk -v name="${archive}" '$2 == name { print $1 }' "${download_dir}/${sums}")"
actual="$(shasum -a 256 "${download_dir}/${archive}" | awk '{print $1}')"
test -n "${expected}" && test "${actual}" = "${expected}"
```

Continue only if the verification command succeeds. Checksums alone detect file
corruption, not substitution. Releases also carry a detached publisher GPG
signature over the checksum file, plus GitHub build-provenance attestations,
verifiable with
`gh attestation verify PATH_TO_ZIP --repo motherduckdb/terraform-provider-motherduck`.

To check the publisher signature, import the public key registered on the
Terraform Registry, then verify the detached signature over the checksum file:

```shell
gh release download "v${version}" \
  --repo motherduckdb/terraform-provider-motherduck \
  --pattern "${sums}.sig" --dir "${download_dir}"
gpg --verify "${download_dir}/${sums}.sig" "${download_dir}/${sums}"
```

## Configure a mirror

Choose a persistent local mirror directory. For example, from your working directory:

```shell
mirror="${PWD}/provider-mirror"
mkdir -p "${mirror}/registry.terraform.io/motherduckdb/motherduck"
cp "${download_dir}/${archive}" "${mirror}/registry.terraform.io/motherduckdb/motherduck/"
```

Create a Terraform CLI configuration file called `provider-installation.tfrc`.
Replace the example path with the absolute value of `mirror` above:

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
runs Terraform. This overrides your usual CLI configuration; include any other
installation settings your environment requires. Do not commit downloaded
binaries or machine-specific paths.

In your Terraform root module, declare:

```hcl
terraform {
  required_providers {
    motherduck = {
      source  = "motherduckdb/motherduck"
      version = "= 0.1.0"
    }
  }
}
provider "motherduck" {}
```

Run `terraform init`, `terraform providers schema -json`, and `terraform validate`.
Terraform may label the mirror package unauthenticated because it lacks a
Registry signing chain. The package should still install and expose its schema.
Commit the resulting dependency lock file. Supply MotherDuck credentials only
when performing authenticated plans or applies.

For CI and remote runners, make the mirror and CLI configuration available on
each runner before `terraform init`. Install the ZIP matching that runner's
platform; a laptop-only mirror is not available remotely.
