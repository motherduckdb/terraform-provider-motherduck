#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OS="$(go env GOOS)"
ARCH="$(go env GOARCH)"

PROVIDER_VERSION="${PROVIDER_VERSION:-0.1.0}"
SOURCE_HOST="registry.terraform.io"
SOURCE_NAMESPACE="motherduckdb"
SOURCE_TYPE="motherduck"
PROVIDER_SOURCE="${SOURCE_NAMESPACE}/${SOURCE_TYPE}"
RUN_ID="${RUN_ID:-$(date +%Y%m%d%H%M%S)_$$}"
TERRAFORM_BIN="${TERRAFORM_BIN:-terraform}"
source "${ROOT_DIR}/scripts/lib/live-common.sh"
source "${ROOT_DIR}/scripts/lib/live-rest.sh"
require_safe_run_id "${RUN_ID}"

if [[ -z "${MOTHERDUCK_ADMIN_TOKEN:-}" ]]; then
  echo "MOTHERDUCK_ADMIN_TOKEN is required for REST permission diagnostics smoke tests" >&2
  exit 1
fi

set +e
preflight_output="$(preflight_rest_admin 2>&1)"
preflight_exit=$?
set -e

if [[ "${preflight_exit}" -eq 0 ]]; then
  echo "Skipping live REST permission diagnostics smoke: MOTHERDUCK_ADMIN_TOKEN is an organization admin."
  exit 0
fi
if [[ "${preflight_exit}" -ne 42 ]]; then
  printf '%s\n' "${preflight_output}" >&2
  exit "${preflight_exit}"
fi

echo "REST admin lifecycle preflight rejected the token as non-admin; validating provider diagnostics."

provider_dir="${PROVIDER_BIN_DIR:-${ROOT_DIR}/tools/provider-bin/${RUN_ID}}"
mirror_dir="${PROVIDER_MIRROR_DIR:-${ROOT_DIR}/tools/provider-mirror/${RUN_ID}}"
mkdir -p "${provider_dir}" "${mirror_dir}/${SOURCE_HOST}/${SOURCE_NAMESPACE}/${SOURCE_TYPE}/${PROVIDER_VERSION}/${OS}_${ARCH}"

provider_binary="${provider_dir}/terraform-provider-${SOURCE_TYPE}_v${PROVIDER_VERSION}"
GOOS="${OS}" GOARCH="${ARCH}" go build -o "${provider_binary}" "${ROOT_DIR}"
cp "${provider_binary}" "${mirror_dir}/${SOURCE_HOST}/${SOURCE_NAMESPACE}/${SOURCE_TYPE}/${PROVIDER_VERSION}/${OS}_${ARCH}/"

work_dir="${ROOT_DIR}/test-results/live-rest-permission-diagnostics-${RUN_ID}"
if [[ -e "${work_dir}" ]]; then
  echo "Refusing to reuse existing test directory: ${work_dir}" >&2
  exit 1
fi
mkdir -p "${work_dir}"
cp "${ROOT_DIR}/test-fixtures/live-rest-permission-diagnostics/main.tf" "${work_dir}/main.tf"
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

echo "==> Live REST permission diagnostics smoke (${RUN_ID})"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" init -backend=false -input=false >/dev/null
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" validate >/dev/null

set +e
apply_output="$(
  TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false -no-color 2>&1
)"
apply_exit=$?
set -e

if [[ "${apply_exit}" -eq 0 ]]; then
  echo "Expected REST permission diagnostics apply to fail for a non-admin token" >&2
  exit 1
fi

if [[ "${apply_output}" != *"Unable to read MotherDuck active accounts"* ||
  "${apply_output}" != *"MotherDuck API error 403"* ||
  "${apply_output}" != *"FORBIDDEN"* ||
  "${apply_output}" != *"minimum role"* ]]; then
  echo "Expected REST permission diagnostic to include the data source, 403, FORBIDDEN, and minimum-role detail, got:" >&2
  printf '%s\n' "${apply_output}" >&2
  exit 1
fi

echo "REST permission diagnostic matched the expected 403 FORBIDDEN response."
