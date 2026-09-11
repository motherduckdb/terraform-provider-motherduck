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

work_dir="${ROOT_DIR}/test-results/live-snapshot-drift-${RUN_ID}"
if [[ -e "${work_dir}" ]]; then
  echo "Refusing to reuse existing test directory: ${work_dir}" >&2
  exit 1
fi
mkdir -p "${work_dir}"
cp "${ROOT_DIR}/test-fixtures/live-snapshot-drift/main.tf" "${work_dir}/main.tf"
perl -0pi -e "s/version = \"= 0\\.1\\.0\"/version = \"= ${PROVIDER_VERSION}\"/" "${work_dir}/main.tf"

cat > "${work_dir}/terraform.tfvars" <<HCL
run_id = "${RUN_ID}"
HCL

cli_config="${work_dir}/terraformrc"
write_provider_cli_config "${cli_config}"

suffix="${RUN_ID//-/_}"
database_name="tf_snapshot_drift_${suffix}"
snapshot_name="tf_snapshot_drift_named_${suffix}"

cleanup() {
  local destroy_status=0
  if [[ "${KEEP_LIVE_FIXTURE}" != "1" && -d "${work_dir}/.terraform" ]]; then
    live_terraform_destroy "${cli_config}" "${TERRAFORM_BIN}" "${work_dir}" || destroy_status=$?
  fi
  if [[ "${KEEP_LIVE_FIXTURE}" != "1" ]]; then
    live_unname_snapshot "${database_name}" "${snapshot_name}" || destroy_status=$?
    live_drop_database "${database_name}" || destroy_status=$?
  fi
  return "${destroy_status}"
}
trap live_cleanup_on_exit EXIT

echo "==> Live snapshot drift smoke (${RUN_ID})"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" init -backend=false -input=false
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" validate
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false

initial_snapshot_id="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -raw snapshot_id)"

go run "${ROOT_DIR}/internal/dev/mdexec" -database "${database_name}" -pre "USE \"${database_name}\"" -sql "ALTER SNAPSHOT '${initial_snapshot_id}' SET snapshot_name = ''"

named_count="$(go run "${ROOT_DIR}/internal/dev/mdexec" -database "${database_name}" -scalar "SELECT count(*)::VARCHAR FROM MD_INFORMATION_SCHEMA.DATABASE_SNAPSHOTS WHERE database_name = '${database_name}' AND snapshot_name = '${snapshot_name}'")"
if [[ "${named_count}" != "0" ]]; then
  echo "Expected out-of-band snapshot unname to remove ${snapshot_name}, found ${named_count}" >&2
  exit 1
fi

expect_drift_plan "Expected Terraform to detect out-of-band snapshot drift with exit code 2, got " \
  env TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" plan -detailed-exitcode -input=false

TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false

repaired_snapshot_id="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -raw snapshot_id)"
if [[ -z "${repaired_snapshot_id}" ]]; then
  echo "Expected snapshot drift repair to restore a named snapshot id" >&2
  exit 1
fi

repaired_named_count="$(go run "${ROOT_DIR}/internal/dev/mdexec" -database "${database_name}" -scalar "SELECT count(*)::VARCHAR FROM MD_INFORMATION_SCHEMA.DATABASE_SNAPSHOTS WHERE database_name = '${database_name}' AND snapshot_name = '${snapshot_name}'")"
if [[ "${repaired_named_count}" != "1" ]]; then
  echo "Expected snapshot drift repair to restore exactly one named snapshot, found ${repaired_named_count}" >&2
  exit 1
fi

expect_noop_plan "Expected no-op plan after repairing snapshot drift, but Terraform reported changes" \
  env TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" plan -detailed-exitcode -input=false

if [[ "${KEEP_LIVE_FIXTURE}" == "1" ]]; then
  trap - EXIT
  echo "Kept live fixture at ${work_dir}"
fi
