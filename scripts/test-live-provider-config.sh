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
ATTACH_MODE="${ATTACH_MODE:-}"

if [[ -z "${MOTHERDUCK_TOKEN:-}" ]]; then
  echo "MOTHERDUCK_TOKEN is required for SQL smoke tests" >&2
  exit 1
fi

database_name="tf_provider_config_${RUN_ID}"

cleanup() {
  local destroy_status=0
  live_drop_database "${database_name}" || destroy_status=$?
  return "${destroy_status}"
}
trap live_cleanup_on_exit EXIT

prepare_provider_mirror

work_dir="${ROOT_DIR}/test-results/live-provider-config-${RUN_ID}"
if [[ -e "${work_dir}" ]]; then
  echo "Refusing to reuse existing test directory: ${work_dir}" >&2
  exit 1
fi
mkdir -p "${work_dir}"
cp "${ROOT_DIR}/test-fixtures/live-provider-config/main.tf" "${work_dir}/main.tf"
perl -0pi -e "s/version = \"= 0\\.1\\.0\"/version = \"= ${PROVIDER_VERSION}\"/" "${work_dir}/main.tf"

cat > "${work_dir}/terraform.tfvars" <<HCL
database_name = "${database_name}"
HCL
if [[ -n "${ATTACH_MODE}" ]]; then
  cat >> "${work_dir}/terraform.tfvars" <<HCL
attach_mode = "${ATTACH_MODE}"
HCL
fi

cli_config="${work_dir}/terraformrc"
write_provider_cli_config "${cli_config}"

if [[ -n "${ATTACH_MODE}" ]]; then
  echo "==> Live provider configuration smoke (${RUN_ID}, attach_mode=${ATTACH_MODE})"
else
  echo "==> Live provider configuration smoke (${RUN_ID})"
fi
go run "${ROOT_DIR}/internal/dev/mdexec" -sql "CREATE DATABASE \"${database_name}\""

TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" init -backend=false -input=false
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" validate
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false

attached_rows_json="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -raw attached_rows_json)"
DATABASE_NAME="${database_name}" ATTACHED_ROWS_JSON="${attached_rows_json}" python3 - <<'PY'
import json
import os
import sys

database_name = os.environ["DATABASE_NAME"].lower()
rows = json.loads(os.environ["ATTACHED_ROWS_JSON"])

for row in rows:
    if any(isinstance(value, str) and value.lower() == database_name for value in row.values()):
        sys.exit(0)

print(f"provider database {database_name} was not found in attached database rows", file=sys.stderr)
sys.exit(1)
PY

set +e
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" plan -detailed-exitcode -input=false
plan_exit=$?
set -e

if [[ "${plan_exit}" -ne 0 ]]; then
  if [[ "${plan_exit}" -eq 2 ]]; then
    echo "Expected no-op plan after provider database attach, but Terraform reported changes" >&2
  fi
  exit "${plan_exit}"
fi

if [[ "${KEEP_LIVE_FIXTURE}" == "1" ]]; then
  trap - EXIT
  echo "Kept live fixture at ${work_dir}"
fi
