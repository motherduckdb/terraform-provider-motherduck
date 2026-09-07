#!/usr/bin/env bash
set -euo pipefail

# Live smoke for the writer-ownership blueprint path:
# stage one mints a writer service account and token with admin credentials,
# stage two applies tenant data infrastructure with the writer token as
# MOTHERDUCK_TOKEN, proving the writer owns the tenant database (write path)
# and the share (GRANT READ ON SHARE to the reader is permitted).

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
  echo "MOTHERDUCK_TOKEN is required for SQL smoke tests" >&2
  exit 1
fi
if [[ -z "${MOTHERDUCK_ADMIN_TOKEN:-}" ]]; then
  echo "MOTHERDUCK_ADMIN_TOKEN is required for writer bootstrap REST operations" >&2
  exit 1
fi

prepare_provider_mirror

result_dir="${ROOT_DIR}/test-results/live-writer-path-${RUN_ID}"
if [[ -e "${result_dir}" ]]; then
  echo "Refusing to reuse existing test directory: ${result_dir}" >&2
  exit 1
fi
bootstrap_dir="${result_dir}/bootstrap"
tenant_dir="${result_dir}/tenant-data"
mkdir -p "${bootstrap_dir}" "${tenant_dir}"
cp "${ROOT_DIR}/test-fixtures/live-writer-bootstrap/main.tf" "${bootstrap_dir}/main.tf"
cp "${ROOT_DIR}/test-fixtures/live-writer-tenant-data/main.tf" "${tenant_dir}/main.tf"
perl -0pi -e "s/version = \"= 0\\.1\\.0\"/version = \"= ${PROVIDER_VERSION}\"/" "${bootstrap_dir}/main.tf" "${tenant_dir}/main.tf"

cli_config="${result_dir}/terraformrc"
write_provider_cli_config "${cli_config}"

suffix="${RUN_ID//-/_}"
writer_username="tf_writer_${suffix}"
tenant_database="tf_writerpath_${suffix}"
tenant_share="tf_writerpath_share_${suffix}"
writer_token=""

cleanup() {
  local destroy_status=0
  if [[ "${KEEP_LIVE_FIXTURE}" != "1" ]]; then
    if [[ -n "${writer_token}" && -d "${tenant_dir}/.terraform" ]]; then
      MOTHERDUCK_TOKEN="${writer_token}" \
        live_terraform_destroy "${cli_config}" "${TERRAFORM_BIN}" "${tenant_dir}" \
        -var "run_id=${RUN_ID}" -var "expected_writer_username=${writer_username}" || destroy_status=$?
      if [[ -n "${writer_token}" ]]; then
        MOTHERDUCK_TOKEN="${writer_token}" live_drop_share "${tenant_share}" || destroy_status=$?
        MOTHERDUCK_TOKEN="${writer_token}" live_drop_database "${tenant_database}" || destroy_status=$?
      fi
    fi
    # Keep the identity and token available to retry failed tenant cleanup.
    if [[ "${destroy_status}" != 0 ]]; then
      echo "Keeping bootstrap credentials because tenant cleanup failed: ${tenant_dir}" >&2
      return "${destroy_status}"
    fi
    if [[ -d "${bootstrap_dir}/.terraform" ]]; then
      live_terraform_destroy "${cli_config}" "${TERRAFORM_BIN}" "${bootstrap_dir}" \
        -var "run_id=${RUN_ID}" || destroy_status=$?
    fi
  fi
  return "${destroy_status}"
}
trap live_cleanup_on_exit EXIT

echo "==> Live writer-path blueprint smoke (${RUN_ID})"

echo "==> Stage 1: writer bootstrap (admin credentials)"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${bootstrap_dir}" init -backend=false -input=false
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${bootstrap_dir}" validate
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${bootstrap_dir}" apply -auto-approve -input=false -var "run_id=${RUN_ID}"

writer_token="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${bootstrap_dir}" output -raw writer_token)"
bootstrap_username="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${bootstrap_dir}" output -raw writer_username)"
if [[ -z "${writer_token}" ]]; then
  echo "Expected stage 1 to output a non-empty writer token" >&2
  exit 1
fi
if [[ "${bootstrap_username}" != "${writer_username}" ]]; then
  echo "Expected writer username ${writer_username}, got ${bootstrap_username}" >&2
  exit 1
fi

echo "==> Stage 2: tenant data plane as the writer identity"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${tenant_dir}" init -backend=false -input=false
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${tenant_dir}" validate
MOTHERDUCK_TOKEN="${writer_token}" \
  TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${tenant_dir}" apply -auto-approve -input=false \
  -var "run_id=${RUN_ID}" -var "expected_writer_username=${writer_username}"

current_user="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${tenant_dir}" output -raw current_user)"
if [[ "${current_user}" != "${writer_username}" ]]; then
  echo "Expected stage 2 SQL identity ${writer_username}, got ${current_user}" >&2
  exit 1
fi

set +e
MOTHERDUCK_TOKEN="${writer_token}" \
  TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${tenant_dir}" plan -detailed-exitcode -input=false \
  -var "run_id=${RUN_ID}" -var "expected_writer_username=${writer_username}"
plan_exit=$?
set -e

if [[ "${plan_exit}" -ne 0 ]]; then
  if [[ "${plan_exit}" -eq 2 ]]; then
    echo "Expected no-op plan after writer-path apply, but Terraform reported changes" >&2
  fi
  exit "${plan_exit}"
fi

if [[ "${KEEP_LIVE_FIXTURE}" == "1" ]]; then
  trap - EXIT
  echo "Kept live fixture at ${result_dir}"
fi
