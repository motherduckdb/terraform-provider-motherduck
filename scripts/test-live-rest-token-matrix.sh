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

if [[ -z "${MOTHERDUCK_ADMIN_TOKEN:-}" ]]; then
  echo "MOTHERDUCK_ADMIN_TOKEN is required for REST admin smoke tests" >&2
  exit 1
fi

source "${ROOT_DIR}/scripts/lib/live-rest.sh"
set +e
preflight_rest_admin
preflight_exit=$?
set -e
if [[ "${preflight_exit}" -eq 42 ]]; then
  echo "REST admin smoke requires an organization-admin MOTHERDUCK_ADMIN_TOKEN" >&2
  exit "${preflight_exit}"
elif [[ "${preflight_exit}" -ne 0 ]]; then
  exit "${preflight_exit}"
fi

prepare_provider_mirror

work_dir="${ROOT_DIR}/test-results/live-rest-token-matrix-${RUN_ID}"
if [[ -e "${work_dir}" ]]; then
  echo "Refusing to reuse existing test directory: ${work_dir}" >&2
  exit 1
fi
mkdir -p "${work_dir}"
cp "${ROOT_DIR}/test-fixtures/live-rest-token-matrix/main.tf" "${work_dir}/main.tf"
perl -0pi -e "s/version = \"= 0\\.1\\.0\"/version = \"= ${PROVIDER_VERSION}\"/" "${work_dir}/main.tf"

cli_config="${work_dir}/terraformrc"
write_provider_cli_config "${cli_config}"

cleanup() {
  local destroy_status=0
  if [[ "${KEEP_LIVE_FIXTURE}" != "1" && -d "${work_dir}/.terraform" ]]; then
    TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" destroy -auto-approve -input=false -var="run_id=${RUN_ID}" || destroy_status=$?
  fi
  return "${destroy_status}"
}
trap live_cleanup_on_exit EXIT

echo "==> Live REST token matrix smoke (${RUN_ID})"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" init -backend=false -input=false
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" validate
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false -var="run_id=${RUN_ID}"

default_type="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -raw default_token_type)"
read_scaling_type="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -raw read_scaling_token_type)"
if [[ "${default_type}" != "read_write" ]]; then
  echo "Expected default token_type to be read_write, got ${default_type}" >&2
  exit 1
fi
if [[ "${read_scaling_type}" != "read_scaling" ]]; then
  echo "Expected read-scaling token_type to be read_scaling, got ${read_scaling_type}" >&2
  exit 1
fi

if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required for the token metadata redaction check" >&2
  exit 1
fi

if TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -json tokens_json | jq -e 'fromjson | any(.[]?; has("token"))' >/dev/null; then
  echo "Token metadata data source unexpectedly exposed token secrets" >&2
  exit 1
fi

expect_noop_plan "Expected no-op plan after token matrix apply, but Terraform reported changes" \
  env TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" plan -detailed-exitcode -input=false -var="run_id=${RUN_ID}"

if [[ "${KEEP_LIVE_FIXTURE}" == "1" ]]; then
  trap - EXIT
  echo "Kept live fixture at ${work_dir}"
fi
