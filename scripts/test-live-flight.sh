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
RUN_FLIGHT="${MD_TF_ACC_ENABLE_FLIGHT_RUNS:-0}"
case "${RUN_FLIGHT}" in
  1 | true | TRUE)
    run_flight_hcl="true"
    ;;
  0 | false | FALSE)
    run_flight_hcl="false"
    ;;
  *)
    echo "MD_TF_ACC_ENABLE_FLIGHT_RUNS must be 0/1 or true/false" >&2
    exit 1
    ;;
esac

if [[ -z "${MOTHERDUCK_TOKEN:-}" ]]; then
  echo "MOTHERDUCK_TOKEN is required for Flight smoke tests" >&2
  exit 1
fi

flight_function_count="$(go run "${ROOT_DIR}/internal/dev/mdexec" -scalar "SELECT count(*)::VARCHAR FROM duckdb_functions() WHERE lower(function_name) = 'md_create_flight'")"
if [[ "${flight_function_count}" == "0" ]]; then
  if [[ "${MD_TF_ACC_REQUIRE_FLIGHTS:-0}" == "1" ]]; then
    echo "Expected MD_CREATE_FLIGHT to be available in this MotherDuck SQL session" >&2
    exit 1
  fi
  echo "Skipping live Flight smoke: MD_CREATE_FLIGHT is not available in this MotherDuck SQL session" >&2
  exit 0
fi

prepare_provider_mirror

work_dir="${ROOT_DIR}/test-results/live-flight-${RUN_ID}"
if [[ -e "${work_dir}" ]]; then
  echo "Refusing to reuse existing test directory: ${work_dir}" >&2
  exit 1
fi
mkdir -p "${work_dir}"
cp "${ROOT_DIR}/test-fixtures/live-flight/main.tf" "${work_dir}/main.tf"
perl -0pi -e "s/version = \"= 0\\.1\\.0\"/version = \"= ${PROVIDER_VERSION}\"/" "${work_dir}/main.tf"

cli_config="${work_dir}/terraformrc"
write_provider_cli_config "${cli_config}"

cleanup() {
  local destroy_status=0
  if [[ "${KEEP_LIVE_FIXTURE}" != "1" && -d "${work_dir}/.terraform" ]]; then
    TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" destroy -auto-approve -input=false || destroy_status=$?
  fi
  return "${destroy_status}"
}
trap live_cleanup_on_exit EXIT

write_vars() {
  local source_label="$1"
  cat > "${work_dir}/terraform.tfvars" <<HCL
run_id = "${RUN_ID}"
source_label = "${source_label}"
run_flight = ${run_flight_hcl}
HCL
}

echo "==> Live Flight smoke (${RUN_ID})"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" init -backend=false -input=false
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" validate

write_vars "initial flight content"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false

write_vars "updated flight content"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false

if [[ "$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -raw max_runtime_sec)" != "300" ]]; then
  echo "Expected Flight max_runtime_sec to refresh as 300" >&2
  exit 1
fi

set +e
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" plan -detailed-exitcode -input=false
plan_exit=$?
set -e

if [[ "${plan_exit}" -ne 0 ]]; then
  if [[ "${plan_exit}" -eq 2 && "${run_flight_hcl}" == "true" ]]; then
    echo "Run-enabled Flight smoke reported a non-empty follow-up plan. Flight run rows are dynamic while runs transition status."
  else
    if [[ "${plan_exit}" -eq 2 ]]; then
      echo "Expected no-op plan after Flight update, but Terraform reported changes" >&2
    fi
    exit "${plan_exit}"
  fi
fi

if [[ "${MD_TF_ACC_ENABLE_FLIGHT_RUNS:-0}" != "1" ]]; then
  # External deletion must lead to recreation, including dependent data sources.
  run_tf() { TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" "$@"; }
  object_id="$(run_tf show -json | python3 -c 'import json,sys; address=sys.argv[1]; print(next(r["values"]["id"] for r in json.load(sys.stdin)["values"]["root_module"]["resources"] if r["address"] == address))' "motherduck_flight.smoke")"
  go run "${ROOT_DIR}/internal/dev/mdexec" -sql "CALL MD_DELETE_FLIGHT(flight_id := '${object_id}'::UUID)"
  drift_status=0
  run_tf plan -detailed-exitcode -input=false || drift_status=$?
  if [[ "${drift_status}" != "2" ]]; then
    echo "Expected a recreation plan after external deletion, got ${drift_status}" >&2
    exit 1
  fi
  run_tf apply -auto-approve -input=false
  run_tf plan -detailed-exitcode -input=false

fi

if [[ "${KEEP_LIVE_FIXTURE}" == "1" ]]; then
  trap - EXIT
  echo "Kept live fixture at ${work_dir}"
fi
