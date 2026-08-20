#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${ROOT_DIR}/scripts/lib/version-matrix.sh"

seen_terraform_versions=()
record_terraform_version() {
  seen_terraform_versions+=("$1")
}

seen_opentofu_versions=()
record_opentofu_version() {
  seen_opentofu_versions+=("$1")
}

TF_VERSION_LIST=()
TOFU_VERSION_LIST=("1.12.3")
dispatch_cli_version_families record_terraform_version record_opentofu_version
if [[ "${#seen_terraform_versions[@]}" -ne 0 ]]; then
  echo "Expected an empty Terraform family to run no Terraform callbacks" >&2
  exit 1
fi
if [[ "${#seen_opentofu_versions[@]}" -ne 1 || "${seen_opentofu_versions[0]}" != "1.12.3" ]]; then
  echo "Expected the configured OpenTofu family to run once" >&2
  exit 1
fi

seen_terraform_versions=()
seen_opentofu_versions=()
TF_VERSION_LIST=("1.5.7" "1.15.6")
TOFU_VERSION_LIST=()
dispatch_cli_version_families record_terraform_version record_opentofu_version
if [[ "${#seen_terraform_versions[@]}" -ne 2 || "${seen_terraform_versions[0]}" != "1.5.7" || "${seen_terraform_versions[1]}" != "1.15.6" ]]; then
  echo "Expected configured Terraform versions to retain their order" >&2
  exit 1
fi
if [[ "${#seen_opentofu_versions[@]}" -ne 0 ]]; then
  echo "Expected an empty OpenTofu family to run no OpenTofu callbacks" >&2
  exit 1
fi
