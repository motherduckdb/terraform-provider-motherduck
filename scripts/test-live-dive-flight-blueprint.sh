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
KEEP_LIVE_FIXTURE="${KEEP_LIVE_FIXTURE:-0}"

if [[ -z "${MOTHERDUCK_TOKEN:-}" ]]; then
  echo "MOTHERDUCK_TOKEN is required for Dive/Flight blueprint smoke tests" >&2
  exit 1
fi

required_functions=(md_create_dive md_create_flight md_run_flight md_list_flight_runs md_get_flight_logs)
for function_name in "${required_functions[@]}"; do
  function_count="$(go run "${ROOT_DIR}/internal/dev/mdexec" -scalar "SELECT count(*)::VARCHAR FROM duckdb_functions() WHERE lower(function_name) = '${function_name}'")"
  if [[ "${function_count}" == "0" ]]; then
    if [[ "${MD_TF_ACC_REQUIRE_DIVE_FLIGHT_BLUEPRINT:-0}" == "1" ]]; then
      echo "Expected ${function_name} to be available in this MotherDuck SQL session" >&2
      exit 1
    fi
    echo "Skipping live Dive/Flight blueprint smoke: ${function_name} is not available in this MotherDuck SQL session" >&2
    exit 0
  fi
done

prepare_provider_mirror

work_dir="${ROOT_DIR}/test-results/live-dive-flight-blueprint-${RUN_ID}"
if [[ -e "${work_dir}" ]]; then
  echo "Refusing to reuse existing test directory: ${work_dir}" >&2
  exit 1
fi
mkdir -p "${work_dir}"
cp "${ROOT_DIR}/test-fixtures/live-dive-flight-blueprint/main.tf" "${work_dir}/main.tf"
perl -0pi -e "s/version = \"= 0\\.1\\.0\"/version = \"= ${PROVIDER_VERSION}\"/" "${work_dir}/main.tf"

cat > "${work_dir}/terraform.tfvars" <<HCL
run_id = "${RUN_ID}"
HCL

cli_config="${work_dir}/terraformrc"
write_provider_cli_config "${cli_config}"

suffix="${RUN_ID//-/_}"
database_name="tf_dive_flight_${suffix}"
share_name="tf_dive_flight_share_${suffix}"

cleanup() {
  local destroy_status=0
  if [[ "${KEEP_LIVE_FIXTURE}" != "1" && -d "${work_dir}/.terraform" ]]; then
    live_terraform_destroy "${cli_config}" "${TERRAFORM_BIN}" "${work_dir}" || destroy_status=$?
  fi
  if [[ "${KEEP_LIVE_FIXTURE}" != "1" ]]; then
    live_drop_share "${share_name}" || destroy_status=$?
    live_drop_database "${database_name}" || destroy_status=$?
  fi
  return "${destroy_status}"
}
trap live_cleanup_on_exit EXIT

echo "==> Live Dive/Flight blueprint smoke (${RUN_ID})"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" init -backend=false -input=false
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" validate
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false

run_status="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -raw flight_run_status)"
normalized_run_status="$(printf '%s' "${run_status}" | tr '[:upper:]' '[:lower:]')"
normalized_run_status="${normalized_run_status#run_status_}"
if [[ "${normalized_run_status}" != "succeeded" ]]; then
  echo "Expected waited Flight run to succeed, got ${run_status}" >&2
  exit 1
fi

flight_logs_ready=false
for _ in {1..12}; do
  log_line_count="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -raw flight_log_line_count)"
  logs_contain_marker="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -raw flight_logs_contain_marker)"
  if [[ "${log_line_count}" =~ ^[1-9][0-9]*$ && "${logs_contain_marker}" == "true" ]]; then
    flight_logs_ready=true
    break
  fi
  sleep 5
  TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -refresh-only -auto-approve -input=false >/dev/null
done
if [[ "${flight_logs_ready}" != "true" ]]; then
  echo "Expected line-oriented Flight logs to contain the blueprint marker after the run completed" >&2
  exit 1
fi

expect_noop_plan "Expected no-op plan after Dive/Flight blueprint apply, but Terraform reported changes" \
  env TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" plan -detailed-exitcode -input=false

if [[ "${KEEP_LIVE_FIXTURE}" == "1" ]]; then
  trap - EXIT
  echo "Kept live fixture at ${work_dir}"
fi
