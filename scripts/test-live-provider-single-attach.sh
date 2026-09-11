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

target_database_name="tf provider single ${RUN_ID}"
excluded_database_name="tf provider excluded ${RUN_ID}"

cleanup() {
  local destroy_status=0
  live_drop_database "${target_database_name}" || destroy_status=$?
  live_drop_database "${excluded_database_name}" || destroy_status=$?
  return "${destroy_status}"
}
trap live_cleanup_on_exit EXIT

prepare_provider_mirror

work_dir="${ROOT_DIR}/test-results/live-provider-single-attach-${RUN_ID}"
if [[ -e "${work_dir}" ]]; then
  echo "Refusing to reuse existing test directory: ${work_dir}" >&2
  exit 1
fi
mkdir -p "${work_dir}"
cp "${ROOT_DIR}/test-fixtures/live-provider-single-attach/main.tf" "${work_dir}/main.tf"
perl -0pi -e "s/version = \"= 0\\.1\\.0\"/version = \"= ${PROVIDER_VERSION}\"/" "${work_dir}/main.tf"

cat > "${work_dir}/terraform.tfvars" <<HCL
database_name = "${target_database_name}"
excluded_database_name = "${excluded_database_name}"
HCL

cli_config="${work_dir}/terraformrc"
write_provider_cli_config "${cli_config}"

echo "==> Live provider single-attach smoke (${RUN_ID})"
go run "${ROOT_DIR}/internal/dev/mdexec" -sql "CREATE DATABASE \"${target_database_name}\""
go run "${ROOT_DIR}/internal/dev/mdexec" -sql "CREATE DATABASE \"${excluded_database_name}\""

TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" init -backend=false -input=false
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" validate
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false

attached_rows_json="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -raw attached_rows_json)"
TARGET_DATABASE_NAME="${target_database_name}" EXCLUDED_DATABASE_NAME="${excluded_database_name}" ATTACHED_ROWS_JSON="${attached_rows_json}" python3 - <<'PY'
import json
import os
import sys

target = os.environ["TARGET_DATABASE_NAME"].lower()
excluded = os.environ["EXCLUDED_DATABASE_NAME"].lower()
rows = json.loads(os.environ["ATTACHED_ROWS_JSON"])

values = [
    value.lower()
    for row in rows
    for value in row.values()
    if isinstance(value, str)
]

if target not in values:
    print(f"provider single attach database {target!r} was not found in attached database rows", file=sys.stderr)
    sys.exit(1)

if excluded in values:
    print(f"single attach unexpectedly included excluded database {excluded!r}", file=sys.stderr)
    sys.exit(1)
PY

expect_noop_plan "Expected no-op plan after provider single attach, but Terraform reported changes" \
  env TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" plan -detailed-exitcode -input=false

if [[ "${KEEP_LIVE_FIXTURE}" == "1" ]]; then
  trap - EXIT
  echo "Kept live fixture at ${work_dir}"
fi
