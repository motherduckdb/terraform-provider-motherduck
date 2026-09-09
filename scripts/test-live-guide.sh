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
  echo "MOTHERDUCK_TOKEN is required for Guide smoke tests" >&2
  exit 1
fi

guide_function_count="$(go run "${ROOT_DIR}/internal/dev/mdexec" -scalar "SELECT count(*)::VARCHAR FROM duckdb_functions() WHERE lower(function_name) = 'md_create_guide'")"
if [[ "${guide_function_count}" == "0" ]]; then
  if [[ "${MD_TF_ACC_REQUIRE_GUIDES:-0}" == "1" ]]; then
    echo "Expected MD_CREATE_GUIDE to be available in this MotherDuck SQL session" >&2
    exit 1
  fi
  echo "Skipping live Guide smoke: MD_CREATE_GUIDE is not available in this MotherDuck SQL session" >&2
  exit 0
fi

prepare_provider_mirror

work_dir="${ROOT_DIR}/test-results/live-guide-${RUN_ID}"
if [[ -e "${work_dir}" ]]; then
  echo "Refusing to reuse existing test directory: ${work_dir}" >&2
  exit 1
fi
mkdir -p "${work_dir}"
cp "${ROOT_DIR}/test-fixtures/live-guide/main.tf" "${work_dir}/main.tf"
perl -0pi -e "s/version = \"= 0\\.1\\.0\"/version = \"= ${PROVIDER_VERSION}\"/" "${work_dir}/main.tf"

cli_config="${work_dir}/terraformrc"
write_provider_cli_config "${cli_config}"

cleanup() {
  local destroy_status=0
  if [[ "${KEEP_LIVE_FIXTURE}" != "1" && -d "${work_dir}/.terraform" ]]; then
    live_terraform_destroy "${cli_config}" "${TERRAFORM_BIN}" "${work_dir}" || destroy_status=$?
  fi
  return "${destroy_status}"
}
trap live_cleanup_on_exit EXIT

write_vars() {
  local content="$1"
  cat > "${work_dir}/terraform.tfvars" <<HCL
run_id = "${RUN_ID}"
content = "${content}"
HCL
}

echo "==> Live Guide smoke (${RUN_ID})"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" init -backend=false -input=false
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" validate

write_vars "# Initial Guide content"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false

foundation_id="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -raw foundation_id)"
reference_uuid="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -raw reference_uuid)"
if [[ "${reference_uuid}" != "${foundation_id}" ]]; then
  echo "Expected the resolved Guide reference UUID to round-trip" >&2
  exit 1
fi

write_vars "# Updated Guide content"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false
if [[ "$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -raw current_version)" != "2" ]]; then
  echo "Expected Guide content update to create version 2" >&2
  exit 1
fi

set +e
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" plan -detailed-exitcode -input=false
plan_exit=$?
set -e
if [[ "${plan_exit}" -ne 0 ]]; then
  if [[ "${plan_exit}" -eq 2 ]]; then
    echo "Expected no-op plan after Guide update, but Terraform reported changes" >&2
  fi
  exit "${plan_exit}"
fi

# External deletion must lead to recreation, including dependent data sources.
run_tf() { TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" "$@"; }
for resource_name in foundation managed; do
  object_id="$(run_tf show -json | python3 -c 'import json,sys; address=sys.argv[1]; print(next(r["values"]["id"] for r in json.load(sys.stdin)["values"]["root_module"]["resources"] if r["address"] == address))' "motherduck_guide.${resource_name}")"
  go run "${ROOT_DIR}/internal/dev/mdexec" -sql "CALL MD_DELETE_GUIDE(id := '${object_id}'::UUID)"
  drift_status=0
  run_tf plan -detailed-exitcode -input=false || drift_status=$?
  if [[ "${drift_status}" != "2" ]]; then
    echo "Expected a recreation plan after external deletion, got ${drift_status}" >&2
    exit 1
  fi
  run_tf apply -auto-approve -input=false
  run_tf plan -detailed-exitcode -input=false
done

if [[ "${KEEP_LIVE_FIXTURE}" == "1" ]]; then
  trap - EXIT
  echo "Kept live fixture at ${work_dir}"
fi
