#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/terraform-test.sh
source "${ROOT_DIR}/scripts/lib/terraform-test.sh"

PROVIDER_VERSION="${PROVIDER_VERSION:-0.1.0}"
RUN_ID="${RUN_ID:-$(date +%Y%m%d%H%M%S)_$$}"
source "${ROOT_DIR}/scripts/lib/live-common.sh"
require_safe_run_id "${RUN_ID}"
TERRAFORM_BIN="${TERRAFORM_BIN:-terraform}"

if [[ -z "${MOTHERDUCK_TOKEN:-}" ]]; then
  echo "MOTHERDUCK_TOKEN is required for SQL function diagnostics smoke tests" >&2
  exit 1
fi

dive_function_count="$(go run "${ROOT_DIR}/internal/dev/mdexec" -scalar "SELECT count(*)::VARCHAR FROM duckdb_functions() WHERE lower(function_name) = 'md_create_dive'")"
if [[ "${dive_function_count}" != "0" ]]; then
  echo "Skipping SQL function diagnostics smoke: MD_CREATE_DIVE is available in this MotherDuck SQL session."
  exit 0
fi

prepare_provider_mirror

work_dir="${ROOT_DIR}/test-results/live-preview-function-diagnostics-${RUN_ID}"
if [[ -e "${work_dir}" ]]; then
  echo "Refusing to reuse existing test directory: ${work_dir}" >&2
  exit 1
fi
mkdir -p "${work_dir}"
cp "${ROOT_DIR}/test-fixtures/live-preview-function-diagnostics/main.tf" "${work_dir}/main.tf"
perl -0pi -e "s/version = \"= 0\\.1\\.0\"/version = \"= ${PROVIDER_VERSION}\"/" "${work_dir}/main.tf"

cat > "${work_dir}/terraform.tfvars" <<HCL
run_id = "${RUN_ID}"
HCL

cli_config="${work_dir}/terraformrc"
write_provider_cli_config "${cli_config}"

echo "==> Live SQL function diagnostics smoke (${RUN_ID})"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" init -backend=false -input=false
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" validate

set +e
apply_output="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false -no-color 2>&1)"
apply_exit=$?
set -e

if [[ "${apply_exit}" -eq 0 ]]; then
  echo "Expected unavailable SQL function apply to fail" >&2
  exit 1
fi
if [[ "${apply_output}" != *"MotherDuck SQL function unavailable"* || "${apply_output}" != *"md_create_dive"* ]]; then
  echo "Expected explicit SQL function diagnostic, got:" >&2
  printf '%s\n' "${apply_output}" >&2
  exit 1
fi
