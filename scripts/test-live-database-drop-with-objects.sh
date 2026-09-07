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

work_dir="${ROOT_DIR}/test-results/live-database-drop-with-objects-${RUN_ID}"
if [[ -e "${work_dir}" ]]; then
  echo "Refusing to reuse existing test directory: ${work_dir}" >&2
  exit 1
fi
mkdir -p "${work_dir}"
cp "${ROOT_DIR}/test-fixtures/live-database-drop-with-objects/main.tf" "${work_dir}/main.tf"
perl -0pi -e "s/version = \"= 0\\.1\\.0\"/version = \"= ${PROVIDER_VERSION}\"/" "${work_dir}/main.tf"

cli_config="${work_dir}/terraformrc"
write_provider_cli_config "${cli_config}"

cat > "${work_dir}/terraform.tfvars" <<HCL
run_id = "${RUN_ID}"
HCL

database_name=""

cleanup() {
  local destroy_status=0
  if [[ "${KEEP_LIVE_FIXTURE}" != "1" && -d "${work_dir}/.terraform" ]]; then
    TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" destroy -auto-approve -input=false || destroy_status=$?
  fi
  if [[ -n "${database_name}" ]]; then
    live_drop_database "${database_name}" || destroy_status=$?
  fi
  return "${destroy_status}"
}
trap live_cleanup_on_exit EXIT

echo "==> Live database drop-with-objects smoke (${RUN_ID})"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" init -backend=false -input=false
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" validate
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false

database_name="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -raw database_name)"

go run "${ROOT_DIR}/internal/dev/mdexec" -database "${database_name}" -sql "CREATE TABLE \"${database_name}\".main.unmanaged_facts (id INTEGER)"
table_count="$(go run "${ROOT_DIR}/internal/dev/mdexec" -database "${database_name}" -scalar "SELECT count(*)::VARCHAR FROM information_schema.tables WHERE table_catalog = '${database_name}' AND table_schema = 'main' AND table_name = 'unmanaged_facts'")"
if [[ "${table_count}" != "1" ]]; then
  echo "Expected unmanaged table to exist before database destroy, got count ${table_count}" >&2
  exit 1
fi

TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" destroy -auto-approve -input=false

database_count="$(go run "${ROOT_DIR}/internal/dev/mdexec" -scalar "SELECT count(*)::VARCHAR FROM MD_INFORMATION_SCHEMA.DATABASES WHERE name = '${database_name}'")"
if [[ "${database_count}" != "0" ]]; then
  echo "Expected database to be gone after Terraform destroy, got count ${database_count}" >&2
  exit 1
fi

if [[ "${KEEP_LIVE_FIXTURE}" == "1" ]]; then
  trap - EXIT
  echo "Kept live fixture at ${work_dir}"
fi
