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
  echo "MOTHERDUCK_TOKEN is required for canonical value smoke tests" >&2
  exit 1
fi

prepare_provider_mirror

work_dir="${ROOT_DIR}/test-results/live-canonical-values-${RUN_ID}"
if [[ -e "${work_dir}" ]]; then
  echo "Refusing to reuse existing test directory: ${work_dir}" >&2
  exit 1
fi
mkdir -p "${work_dir}"
cp "${ROOT_DIR}/test-fixtures/live-canonical-values/main.tf" "${work_dir}/main.tf"
perl -0pi -e "s/version = \"= 0\\.1\\.0\"/version = \"= ${PROVIDER_VERSION}\"/" "${work_dir}/main.tf"

cat > "${work_dir}/terraform.tfvars" <<HCL
run_id = "${RUN_ID}"
HCL

cli_config="${work_dir}/terraformrc"
write_provider_cli_config "${cli_config}"

suffix="${RUN_ID//-/_}"
database_name="tf_canonical_${suffix}"
share_name="tf_canonical_share_${suffix}"
secret_name="tf_canonical_secret_${suffix}"

cleanup() {
  local destroy_status=0
  if [[ "${KEEP_LIVE_FIXTURE}" != "1" && -d "${work_dir}/.terraform" ]]; then
    TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" destroy -auto-approve -input=false || destroy_status=$?
  fi
  if [[ "${KEEP_LIVE_FIXTURE}" != "1" ]]; then
    live_drop_share "${share_name}" || destroy_status=$?
    live_drop_secret "${secret_name}" || destroy_status=$?
    live_drop_database "${database_name}" || destroy_status=$?
  fi
  return "${destroy_status}"
}
trap live_cleanup_on_exit EXIT

echo "==> Live canonical value smoke (${RUN_ID})"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" init -backend=false -input=false
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" validate
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false

assert_output() {
  local name="$1"
  local want="$2"
  local got
  got="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -raw "${name}")"
  if [[ "${got}" != "${want}" ]]; then
    echo "Expected ${name}=${want}, got ${got}" >&2
    exit 1
  fi
}

assert_output database_type default
assert_output share_access restricted
assert_output share_visibility hidden
assert_output share_update_mode automatic
assert_output secret_type s3

expect_noop_plan "Expected no-op plan after canonical value apply, but Terraform reported changes" \
  env TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" plan -detailed-exitcode -input=false

if [[ "${KEEP_LIVE_FIXTURE}" == "1" ]]; then
  trap - EXIT
  echo "Kept live fixture at ${work_dir}"
fi
