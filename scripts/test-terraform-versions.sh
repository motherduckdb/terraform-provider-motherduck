#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OS="$(go env GOOS)"
ARCH="$(go env GOARCH)"

TF_VERSIONS="${TF_VERSIONS-1.5.7 1.8.5 1.12.2 1.15.8}"
TOFU_VERSIONS="${TOFU_VERSIONS-1.12.5}"
TF_VERSION_LIST=()
while IFS= read -r version; do
  if [[ -n "${version}" ]]; then
    if [[ ! "${version}" =~ ^[0-9]+[.][0-9]+[.][0-9]+$ ]]; then
      echo "Invalid Terraform version '${version}'. Use versions like 1.8.5, separated by spaces or commas." >&2
      exit 1
    fi
    TF_VERSION_LIST+=("${version}")
  fi
done < <(printf '%s\n' "${TF_VERSIONS//,/ }" | tr '[:space:]' '\n')
TOFU_VERSION_LIST=()
while IFS= read -r version; do
  if [[ -n "${version}" ]]; then
    if [[ ! "${version}" =~ ^[0-9]+[.][0-9]+[.][0-9]+$ ]]; then
      echo "Invalid OpenTofu version '${version}'. Use versions like 1.12.5, separated by spaces or commas." >&2
      exit 1
    fi
    TOFU_VERSION_LIST+=("${version}")
  fi
done < <(printf '%s\n' "${TOFU_VERSIONS//,/ }" | tr '[:space:]' '\n')

if [[ "${#TF_VERSION_LIST[@]}" -eq 0 && "${#TOFU_VERSION_LIST[@]}" -eq 0 ]]; then
  echo "TF_VERSIONS and TOFU_VERSIONS did not contain any versions" >&2
  exit 1
fi
PROVIDER_VERSION="${PROVIDER_VERSION:-0.1.0}"
TF_VERSION_SQL_LIFECYCLE="${TF_VERSION_SQL_LIFECYCLE:-0}"
TF_VERSION_BLUEPRINT_LIFECYCLE="${TF_VERSION_BLUEPRINT_LIFECYCLE:-0}"
SOURCE_HOSTS=("registry.terraform.io" "registry.opentofu.org")
SOURCE_NAMESPACE="motherduckdb"
SOURCE_TYPE="motherduck"
PROVIDER_SOURCE="${SOURCE_NAMESPACE}/${SOURCE_TYPE}"
RUN_ID="${RUN_ID:-$(date +%Y%m%d%H%M%S)_$$}"
source "${ROOT_DIR}/scripts/lib/live-common.sh"
source "${ROOT_DIR}/scripts/lib/download-checksum.sh"
source "${ROOT_DIR}/scripts/lib/version-matrix.sh"
require_safe_run_id "${RUN_ID}"

if [[ -z "${MOTHERDUCK_TOKEN:-}" ]]; then
  echo "MOTHERDUCK_TOKEN is required for SQL smoke tests" >&2
  exit 1
fi

if [[ -z "${MOTHERDUCK_ADMIN_TOKEN:-}" ]]; then
  echo "MOTHERDUCK_ADMIN_TOKEN is not set; running Terraform version matrix in SQL-only mode."
  rest_admin_available=false
  rest_admin_skip_reason="MOTHERDUCK_ADMIN_TOKEN is not set"
else
  source "${ROOT_DIR}/scripts/lib/live-rest.sh"
  set +e
  preflight_rest_admin
  preflight_exit=$?
  set -e
  if [[ "${preflight_exit}" -eq 0 ]]; then
    rest_admin_available=true
    rest_admin_skip_reason=""
  elif [[ "${preflight_exit}" -eq 42 ]]; then
    rest_admin_available=false
    rest_admin_skip_reason="MOTHERDUCK_ADMIN_TOKEN can authenticate, but is not an organization admin"
  else
    exit "${preflight_exit}"
  fi
fi

provider_dir="${PROVIDER_BIN_DIR:-${ROOT_DIR}/tools/provider-bin/${RUN_ID}}"
mirror_dir="${PROVIDER_MIRROR_DIR:-${ROOT_DIR}/tools/provider-mirror/${RUN_ID}}"
mkdir -p "${provider_dir}"

provider_binary="${provider_dir}/terraform-provider-${SOURCE_TYPE}_v${PROVIDER_VERSION}"
GOOS="${OS}" GOARCH="${ARCH}" go build -o "${provider_binary}" "${ROOT_DIR}"
for source_host in "${SOURCE_HOSTS[@]}"; do
  mirror_package_dir="${mirror_dir}/${source_host}/${SOURCE_NAMESPACE}/${SOURCE_TYPE}/${PROVIDER_VERSION}/${OS}_${ARCH}"
  mkdir -p "${mirror_package_dir}"
  cp "${provider_binary}" "${mirror_package_dir}/"
done

run_cli_smoke() {
  local cli_name="$1"
  local version="$2"
  local terraform_bin="$3"

  local work_dir="${ROOT_DIR}/test-results/${cli_name}-${version}-${RUN_ID}"
  if [[ -e "${work_dir}" ]]; then
    echo "Refusing to reuse existing test directory: ${work_dir}" >&2
    exit 1
  fi
  mkdir -p "${work_dir}"
  cp "${ROOT_DIR}/test-fixtures/read-only-smoke/main.tf" "${work_dir}/main.tf"
  perl -0pi -e "s/version = \"= 0\\.1\\.0\"/version = \"= ${PROVIDER_VERSION}\"/" "${work_dir}/main.tf"

  cli_config="${work_dir}/terraformrc"
  cat > "${cli_config}" <<HCL
provider_installation {
  filesystem_mirror {
    path    = "${mirror_dir}"
    include = ["${PROVIDER_SOURCE}"]
  }
  direct {
    exclude = ["${PROVIDER_SOURCE}"]
  }
}
HCL

  echo "==> ${cli_name} ${version}"
  TF_CLI_CONFIG_FILE="${cli_config}" "${terraform_bin}" -chdir="${work_dir}" init -backend=false -input=false
  TF_CLI_CONFIG_FILE="${cli_config}" "${terraform_bin}" -chdir="${work_dir}" validate
  TF_CLI_CONFIG_FILE="${cli_config}" "${terraform_bin}" -chdir="${work_dir}" apply -auto-approve -input=false -var="enable_rest=${rest_admin_available}"

  version_run_id="${RUN_ID}-${cli_name}${version//./}"
  if [[ "${TF_VERSION_SQL_LIFECYCLE}" == "1" ]]; then
    TERRAFORM_BIN="${terraform_bin}" RUN_ID="${version_run_id}" "${ROOT_DIR}/scripts/test-live-database-drop-with-objects.sh"
  fi

  if [[ "${TF_VERSION_BLUEPRINT_LIFECYCLE}" == "1" ]]; then
    TERRAFORM_BIN="${terraform_bin}" RUN_ID="${version_run_id}" "${ROOT_DIR}/scripts/test-live-blueprint-sql-only.sh"
  fi

  if [[ "${rest_admin_available}" == "true" ]]; then
    TERRAFORM_BIN="${terraform_bin}" RUN_ID="${version_run_id}" "${ROOT_DIR}/scripts/test-live-rest-token-matrix.sh"
  else
    echo "Skipping ${cli_name} ${version} REST token matrix because ${rest_admin_skip_reason}."
  fi
}

run_terraform_version() {
  local version="$1"
  local terraform_bin="${ROOT_DIR}/tools/terraform/${version}/terraform"
  local terraform_verified_marker="${terraform_bin}.checksum-verified"
  if [[ ! -x "${terraform_bin}" || ! -f "${terraform_verified_marker}" ]]; then
    local archive="terraform_${version}_${OS}_${ARCH}.zip"
    local archive_path="${ROOT_DIR}/tools/terraform/${version}/${archive}"
    local release_url="https://releases.hashicorp.com/terraform/${version}"
    download_verified_archive \
      "${release_url}/${archive}" \
      "${release_url}/terraform_${version}_SHA256SUMS" \
      "${archive_path}"
    unzip -q -o "${archive_path}" -d "$(dirname "${terraform_bin}")"
    if [[ ! -x "${terraform_bin}" ]]; then
      echo "Terraform ${version} archive did not install an executable at ${terraform_bin}" >&2
      exit 1
    fi
    touch "${terraform_verified_marker}"
  fi
  run_cli_smoke "terraform" "${version}" "${terraform_bin}"
}

run_opentofu_version() {
  local version="$1"
  local tofu_bin="${ROOT_DIR}/tools/opentofu/${version}/tofu"
  local tofu_verified_marker="${tofu_bin}.checksum-verified"
  if [[ ! -x "${tofu_bin}" || ! -f "${tofu_verified_marker}" ]]; then
    local archive="tofu_${version}_${OS}_${ARCH}.zip"
    local archive_path="${ROOT_DIR}/tools/opentofu/${version}/${archive}"
    local release_url="https://github.com/opentofu/opentofu/releases/download/v${version}"
    download_verified_archive \
      "${release_url}/${archive}" \
      "${release_url}/tofu_${version}_SHA256SUMS" \
      "${archive_path}"
    unzip -q -o "${archive_path}" -d "$(dirname "${tofu_bin}")"
    if [[ ! -x "${tofu_bin}" ]]; then
      echo "OpenTofu ${version} archive did not install an executable at ${tofu_bin}" >&2
      exit 1
    fi
    touch "${tofu_verified_marker}"
  fi
  run_cli_smoke "opentofu" "${version}" "${tofu_bin}"
}

dispatch_cli_version_families run_terraform_version run_opentofu_version
