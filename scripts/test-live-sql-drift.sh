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
  echo "MOTHERDUCK_TOKEN is required for SQL smoke tests" >&2
  exit 1
fi

prepare_provider_mirror

work_dir="${ROOT_DIR}/test-results/live-sql-drift-${RUN_ID}"
if [[ -e "${work_dir}" ]]; then
  echo "Refusing to reuse existing test directory: ${work_dir}" >&2
  exit 1
fi
mkdir -p "${work_dir}"
cp "${ROOT_DIR}/test-fixtures/live-sql-drift/main.tf" "${work_dir}/main.tf"
perl -0pi -e "s/version = \"= 0\\.1\\.0\"/version = \"= ${PROVIDER_VERSION}\"/" "${work_dir}/main.tf"

cat > "${work_dir}/terraform.tfvars" <<HCL
run_id = "${RUN_ID}"
HCL

cli_config="${work_dir}/terraformrc"
write_provider_cli_config "${cli_config}"

suffix="${RUN_ID//-/_}"
database_name="tf_drift_${suffix}"
schema_name="app"
table_name="facts"
share_name="tf_drift_share_${suffix}"
secret_name="tf_drift_s3_${suffix}"

cleanup() {
  local destroy_status=0
  if [[ "${KEEP_LIVE_FIXTURE}" != "1" && -d "${work_dir}/.terraform" ]]; then
    live_terraform_destroy "${cli_config}" "${TERRAFORM_BIN}" "${work_dir}" || destroy_status=$?
  fi
  if [[ "${KEEP_LIVE_FIXTURE}" != "1" ]]; then
    live_drop_share "${share_name}" || destroy_status=$?
    live_drop_secret "${secret_name}" || destroy_status=$?
    live_drop_database "${database_name}" || destroy_status=$?
  fi
  return "${destroy_status}"
}
trap live_cleanup_on_exit EXIT

echo "==> Live SQL drift smoke (${RUN_ID})"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" init -backend=false -input=false
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" validate
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false

go run "${ROOT_DIR}/internal/dev/mdexec" -sql "DROP SHARE IF EXISTS \"${share_name}\""
go run "${ROOT_DIR}/internal/dev/mdexec" -database "${database_name}" -sql "DROP TABLE \"${database_name}\".\"${schema_name}\".\"${table_name}\""
go run "${ROOT_DIR}/internal/dev/mdexec" -sql "DROP SECRET \"${secret_name}\" FROM motherduck"

set +e
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" plan -detailed-exitcode -input=false
drift_plan_exit=$?
set -e

if [[ "${drift_plan_exit}" -ne 2 ]]; then
  echo "Expected Terraform to detect out-of-band SQL drift with exit code 2, got ${drift_plan_exit}" >&2
  exit 1
fi

TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false

expect_noop_plan "Expected no-op plan after repairing SQL drift, but Terraform reported changes" \
  env TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" plan -detailed-exitcode -input=false

if [[ "${KEEP_LIVE_FIXTURE}" == "1" ]]; then
  trap - EXIT
  echo "Kept live fixture at ${work_dir}"
fi
