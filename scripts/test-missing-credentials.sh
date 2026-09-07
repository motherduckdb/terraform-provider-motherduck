#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/terraform-test.sh
source "${ROOT_DIR}/scripts/lib/terraform-test.sh"
isolate_offline_test_environment

PROVIDER_VERSION="${PROVIDER_VERSION:-0.1.0}"
RUN_ID="${RUN_ID:-$(date +%Y%m%d%H%M%S)_$$}"
TERRAFORM_BIN="${TERRAFORM_BIN:-terraform}"

prepare_provider_mirror

echo "==> Missing credential diagnostics smoke (${RUN_ID})"

run_case() {
  local case_name="$1"
  local expected_summary="$2"
  local expected_detail="$3"
  local fixture_dir="${ROOT_DIR}/test-fixtures/${case_name}"
  local work_dir="${ROOT_DIR}/test-results/${case_name}-${RUN_ID}"

  if [[ -e "${work_dir}" ]]; then
    echo "Refusing to reuse existing test directory: ${work_dir}" >&2
    exit 1
  fi
  mkdir -p "${work_dir}"
  cp "${fixture_dir}/main.tf" "${work_dir}/main.tf"
  perl -0pi -e "s/version = \"= 0\\.1\\.0\"/version = \"= ${PROVIDER_VERSION}\"/" "${work_dir}/main.tf"

  local cli_config="${work_dir}/terraformrc"
  write_provider_cli_config "${cli_config}"

  TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" init -backend=false -input=false >/dev/null
  TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" validate >/dev/null

  set +e
  apply_output="$(
    (
      unset MOTHERDUCK_TOKEN
      unset MOTHERDUCK_ADMIN_TOKEN
      TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false -no-color
    ) 2>&1
  )"
  apply_exit=$?
  set -e

  if [[ "${apply_exit}" -eq 0 ]]; then
    echo "Expected ${case_name} apply to fail without credentials" >&2
    exit 1
  fi
  if [[ "${apply_output}" != *"${expected_summary}"* || "${apply_output}" != *"${expected_detail}"* ]]; then
    echo "Expected ${case_name} diagnostic to include ${expected_summary} and ${expected_detail}, got:" >&2
    printf '%s\n' "${apply_output}" >&2
    exit 1
  fi
}

run_case "missing-sql-token" "MotherDuck token required" "MotherDuck SQL operations require token or MOTHERDUCK_TOKEN"
run_case "missing-admin-token" "MotherDuck admin token required" "MotherDuck REST operations require admin_token or MOTHERDUCK_ADMIN_TOKEN"
