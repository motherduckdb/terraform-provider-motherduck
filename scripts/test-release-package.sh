#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
jq -e '.version == 1 and .metadata.protocol_versions == ["6.0"]' \
  "${ROOT_DIR}/terraform-registry-manifest.json" >/dev/null

# Install the actual packed artifact, never a freshly rebuilt binary.
archive="$1"
version="${2#v}"
terraform_bin="${TERRAFORM_BIN:-terraform}"
work_dir="$(mktemp -d)"
trap 'rm -rf "${work_dir}"' EXIT
archive="$(cd "$(dirname "${archive}")" && pwd)/$(basename "${archive}")"
binary="terraform-provider-motherduck_v${version}"
if [[ "$(unzip -Z1 "${archive}")" != "${binary}" ]]; then
  echo "Release archive must contain exactly ${binary}" >&2
  exit 1
fi
unzip -q "${archive}" -d "${work_dir}/unpacked"
test -x "${work_dir}/unpacked/${binary}"
platform="$(go env GOOS)_$(go env GOARCH)"
mirror="${work_dir}/mirror/registry.terraform.io/motherduckdb/motherduck"
mkdir -p "${mirror}" "${work_dir}/config"
cp "${archive}" "${mirror}/terraform-provider-motherduck_${version}_${platform}.zip"
cat > "${work_dir}/terraformrc" <<HCL
provider_installation {
  filesystem_mirror {
    path = "${work_dir}/mirror"
    include = ["registry.terraform.io/motherduckdb/motherduck"]
  }
}
HCL
cat > "${work_dir}/config/main.tf" <<HCL
terraform {
  required_providers {
    motherduck = {
      source = "motherduckdb/motherduck"
      version = "= ${version}"
    }
  }
}
provider "motherduck" {}
HCL
export TF_CLI_CONFIG_FILE="${work_dir}/terraformrc"
unset TF_REATTACH_PROVIDERS TF_PLUGIN_CACHE_DIR
"${terraform_bin}" -chdir="${work_dir}/config" init -backend=false -input=false
"${terraform_bin}" -chdir="${work_dir}/config" providers schema -json > "${work_dir}/schema.json"
jq -e '.provider_schemas["registry.terraform.io/motherduckdb/motherduck"] |
  (.resource_schemas.motherduck_database != null) and
  (.data_source_schemas.motherduck_current_user != null)' "${work_dir}/schema.json" >/dev/null
"${terraform_bin}" -chdir="${work_dir}/config" validate
