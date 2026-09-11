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

work_dir="${ROOT_DIR}/test-results/live-share-modes-${RUN_ID}"
if [[ -e "${work_dir}" ]]; then
  echo "Refusing to reuse existing test directory: ${work_dir}" >&2
  exit 1
fi
mkdir -p "${work_dir}"
cp "${ROOT_DIR}/test-fixtures/live-share-modes/main.tf" "${work_dir}/main.tf"
perl -0pi -e "s/version = \"= 0\\.1\\.0\"/version = \"= ${PROVIDER_VERSION}\"/" "${work_dir}/main.tf"

cat > "${work_dir}/terraform.tfvars" <<HCL
run_id = "${RUN_ID}"
HCL

cli_config="${work_dir}/terraformrc"
write_provider_cli_config "${cli_config}"

suffix="${RUN_ID//-/_}"
database_name="tf_share_modes_${suffix}"
share_name="tf_share_modes_share_${suffix}"

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

echo "==> Live share mode smoke (${RUN_ID})"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" init -backend=false -input=false
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" validate
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false

if [[ "$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -raw share_access)" != "organization" ]]; then
  echo "Expected share access to refresh as organization" >&2
  exit 1
fi
if [[ "$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -raw share_visibility)" != "discoverable" ]]; then
  echo "Expected share visibility to refresh as discoverable" >&2
  exit 1
fi
if [[ "$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -raw share_update_mode)" != "manual" ]]; then
  echo "Expected share update mode to refresh as manual" >&2
  exit 1
fi

share_rows_json="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -raw share_rows_json)"
SHARE_ROWS_JSON="${share_rows_json}" python3 - <<'PY'
import json
import os
import sys

rows = json.loads(os.environ["SHARE_ROWS_JSON"])
if len(rows) != 1:
    print(f"expected one owned share row, got {len(rows)}", file=sys.stderr)
    sys.exit(1)

row = rows[0]
expected = {
    "access": "ORGANIZATION",
    "visibility": "DISCOVERABLE",
    "update": "MANUAL",
}
for key, value in expected.items():
    if row.get(key) != value:
        print(f"expected raw share row {key}={value}, got {row.get(key)!r}", file=sys.stderr)
        sys.exit(1)
PY

expect_noop_plan "Expected no-op plan after share mode refresh, but Terraform reported changes" \
  env TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" plan -detailed-exitcode -input=false

if [[ "${KEEP_LIVE_FIXTURE}" == "1" ]]; then
  trap - EXIT
  echo "Kept live fixture at ${work_dir}"
fi
