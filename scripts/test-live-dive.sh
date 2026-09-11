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
  echo "MOTHERDUCK_TOKEN is required for Dive smoke tests" >&2
  exit 1
fi

dive_function_count="$(go run "${ROOT_DIR}/internal/dev/mdexec" -scalar "SELECT count(*)::VARCHAR FROM duckdb_functions() WHERE lower(function_name) = 'md_create_dive'")"
if [[ "${dive_function_count}" == "0" ]]; then
  if [[ "${MD_TF_ACC_REQUIRE_DIVES:-0}" == "1" ]]; then
    echo "Expected MD_CREATE_DIVE to be available in this MotherDuck SQL session" >&2
    exit 1
  fi
  echo "Skipping live Dive smoke: MD_CREATE_DIVE is not available in this MotherDuck SQL session" >&2
  exit 0
fi

prepare_provider_mirror

work_dir="${ROOT_DIR}/test-results/live-dive-${RUN_ID}"
if [[ -e "${work_dir}" ]]; then
  echo "Refusing to reuse existing test directory: ${work_dir}" >&2
  exit 1
fi
mkdir -p "${work_dir}"
cp "${ROOT_DIR}/test-fixtures/live-dive/main.tf" "${work_dir}/main.tf"
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
  local title_suffix="$1"
  local description="$2"
  local content_label="$3"
  local status="$4"
  cat > "${work_dir}/terraform.tfvars" <<HCL
run_id = "${RUN_ID}"
title_suffix = "${title_suffix}"
description = "${description}"
content_label = "${content_label}"
status = ${status}
HCL
}

assert_status() {
  local expected="$1"
  local actual
  actual="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -raw status)"
  if [[ "${actual}" != "${expected}" ]]; then
    echo "Expected Dive status ${expected}, got ${actual}" >&2
    exit 1
  fi
}

echo "==> Live Dive smoke (${RUN_ID})"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" init -backend=false -input=false
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" validate

write_vars "initial" "Initial Terraform Dive smoke" "Initial content" "null"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false
assert_status "draft"

write_vars "ready" "Ready Terraform Dive smoke" "Ready content" '"ready"'
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false
assert_status "ready"

write_vars "archived" "Archived Terraform Dive smoke" "Archived content" '"archived"'
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false
assert_status "archived"

expect_noop_plan "Expected no-op plan after Dive update, but Terraform reported changes" \
  env TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" plan -detailed-exitcode -input=false

# External deletion must lead to recreation, including dependent data sources.
run_tf() { TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" "$@"; }
object_id="$(run_tf show -json | python3 -c 'import json,sys; address=sys.argv[1]; print(next(r["values"]["id"] for r in json.load(sys.stdin)["values"]["root_module"]["resources"] if r["address"] == address))' "motherduck_dive.smoke")"
go run "${ROOT_DIR}/internal/dev/mdexec" -sql "CALL MD_DELETE_DIVE(id := '${object_id}'::UUID)"
drift_status=0
run_tf plan -detailed-exitcode -input=false || drift_status=$?
if [[ "${drift_status}" != "2" ]]; then
  echo "Expected a recreation plan after external deletion, got ${drift_status}" >&2
  exit 1
fi
run_tf apply -auto-approve -input=false
run_tf plan -detailed-exitcode -input=false

if [[ "${KEEP_LIVE_FIXTURE}" == "1" ]]; then
  trap - EXIT
  echo "Kept live fixture at ${work_dir}"
fi
