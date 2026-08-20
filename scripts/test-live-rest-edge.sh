#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OS="$(go env GOOS)"
ARCH="$(go env GOARCH)"

PROVIDER_VERSION="${PROVIDER_VERSION:-0.1.0}"
SOURCE_HOST="registry.terraform.io"
SOURCE_NAMESPACE="motherduckdb"
SOURCE_TYPE="motherduck"
PROVIDER_SOURCE="${SOURCE_NAMESPACE}/${SOURCE_TYPE}"
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
  exit 0
elif [[ "${preflight_exit}" -ne 0 ]]; then
  exit "${preflight_exit}"
fi

provider_dir="${PROVIDER_BIN_DIR:-${ROOT_DIR}/tools/provider-bin/${RUN_ID}}"
mirror_dir="${PROVIDER_MIRROR_DIR:-${ROOT_DIR}/tools/provider-mirror/${RUN_ID}}"
mkdir -p "${provider_dir}" "${mirror_dir}/${SOURCE_HOST}/${SOURCE_NAMESPACE}/${SOURCE_TYPE}/${PROVIDER_VERSION}/${OS}_${ARCH}"

provider_binary="${provider_dir}/terraform-provider-${SOURCE_TYPE}_v${PROVIDER_VERSION}"
GOOS="${OS}" GOARCH="${ARCH}" go build -o "${provider_binary}" "${ROOT_DIR}"
cp "${provider_binary}" "${mirror_dir}/${SOURCE_HOST}/${SOURCE_NAMESPACE}/${SOURCE_TYPE}/${PROVIDER_VERSION}/${OS}_${ARCH}/"

work_dir="${ROOT_DIR}/test-results/live-rest-edge-${RUN_ID}"
import_dir="${ROOT_DIR}/test-results/live-rest-edge-import-${RUN_ID}"
if [[ -e "${work_dir}" || -e "${import_dir}" ]]; then
  echo "Refusing to reuse existing test directory for run id: ${RUN_ID}" >&2
  exit 1
fi
mkdir -p "${work_dir}" "${import_dir}"
cp "${ROOT_DIR}/test-fixtures/live-rest-edge/main.tf" "${work_dir}/main.tf"
cp "${ROOT_DIR}/test-fixtures/live-rest-edge-import/main.tf" "${import_dir}/main.tf"
perl -0pi -e "s/version = \"= 0\\.1\\.0\"/version = \"= ${PROVIDER_VERSION}\"/" "${work_dir}/main.tf" "${import_dir}/main.tf"

cli_config="${work_dir}/terraformrc"
cat > "${cli_config}" <<HCL
provider_installation {
  filesystem_mirror {
    path    = "${mirror_dir}"
    include = ["${PROVIDER_SOURCE}"]
  }
  direct {
    exclude = ["${PROVIDER_SOURCE}"]
  }
}
HCL
cp "${cli_config}" "${import_dir}/terraformrc"

write_vars() {
  local path="$1"
  local token_name="$2"
  local token_ttl="$3"
  local rw_cooldown="$4"
  local rs_flock_size="$5"
  local rs_cooldown="$6"
  cat > "${path}" <<HCL
run_id = "${RUN_ID}"
token_name = "${token_name}"
token_ttl = ${token_ttl}
read_write_instance_size = "standard"
read_write_cooldown_seconds = ${rw_cooldown}
read_scaling_instance_size = "standard"
read_scaling_flock_size = ${rs_flock_size}
read_scaling_cooldown_seconds = ${rs_cooldown}
HCL
}

cleanup() {
  if [[ "${KEEP_LIVE_FIXTURE}" != "1" && -d "${work_dir}/.terraform" ]]; then
    TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" destroy -auto-approve -input=false
  fi
}
trap cleanup EXIT

initial_vars="${work_dir}/terraform.tfvars"
updated_vars="${work_dir}/terraform.tfvars"
write_vars "${initial_vars}" "terraform-rest-edge-initial" 3600 60 2 120

echo "==> Live REST edge smoke (${RUN_ID})"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" init -backend=false -input=false
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" validate
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false

write_vars "${updated_vars}" "terraform-rest-edge-updated" 7200 90 3 180
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false

set +e
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" plan -detailed-exitcode -input=false
plan_exit=$?
set -e

if [[ "${plan_exit}" -ne 0 ]]; then
  if [[ "${plan_exit}" -eq 2 ]]; then
    echo "Expected no-op plan after REST update, but Terraform reported changes" >&2
  fi
  exit "${plan_exit}"
fi

username="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -raw username)"
token_id="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -raw token_id)"
token_name="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -raw token_name)"
token_type="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -raw token_type)"

import_vars="${import_dir}/terraform.tfvars"
cat > "${import_vars}" <<HCL
username = "${username}"
token_id = "${token_id}"
token_name = "${token_name}"
token_type = "${token_type}"
read_write_instance_size = "standard"
read_write_cooldown_seconds = 90
read_scaling_instance_size = "standard"
read_scaling_flock_size = 3
read_scaling_cooldown_seconds = 180
HCL

TF_CLI_CONFIG_FILE="${import_dir}/terraformrc" "${TERRAFORM_BIN}" -chdir="${import_dir}" init -backend=false -input=false
TF_CLI_CONFIG_FILE="${import_dir}/terraformrc" "${TERRAFORM_BIN}" -chdir="${import_dir}" import -input=false motherduck_service_account.imported "${username}"
TF_CLI_CONFIG_FILE="${import_dir}/terraformrc" "${TERRAFORM_BIN}" -chdir="${import_dir}" import -input=false motherduck_duckling_config.imported "${username}"
TF_CLI_CONFIG_FILE="${import_dir}/terraformrc" "${TERRAFORM_BIN}" -chdir="${import_dir}" import -input=false motherduck_access_token.imported "${username}/${token_id}"

set +e
TF_CLI_CONFIG_FILE="${import_dir}/terraformrc" "${TERRAFORM_BIN}" -chdir="${import_dir}" plan -detailed-exitcode -input=false
import_plan_exit=$?
set -e

if [[ "${import_plan_exit}" -ne 0 ]]; then
  if [[ "${import_plan_exit}" -eq 2 ]]; then
    echo "Expected no-op plan after REST import, but Terraform reported changes" >&2
  fi
  exit "${import_plan_exit}"
fi

if [[ "${KEEP_LIVE_FIXTURE}" == "1" ]]; then
  trap - EXIT
  echo "Kept live fixture at ${work_dir}"
else
  trap - EXIT
  cleanup
fi
