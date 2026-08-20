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

if [[ -z "${MOTHERDUCK_TOKEN:-}" ]]; then
  echo "MOTHERDUCK_TOKEN is required for SQL smoke tests" >&2
  exit 1
fi

provider_dir="${PROVIDER_BIN_DIR:-${ROOT_DIR}/tools/provider-bin/${RUN_ID}}"
mirror_dir="${PROVIDER_MIRROR_DIR:-${ROOT_DIR}/tools/provider-mirror/${RUN_ID}}"
mkdir -p "${provider_dir}" "${mirror_dir}/${SOURCE_HOST}/${SOURCE_NAMESPACE}/${SOURCE_TYPE}/${PROVIDER_VERSION}/${OS}_${ARCH}"

provider_binary="${provider_dir}/terraform-provider-${SOURCE_TYPE}_v${PROVIDER_VERSION}"
GOOS="${OS}" GOARCH="${ARCH}" go build -o "${provider_binary}" "${ROOT_DIR}"
cp "${provider_binary}" "${mirror_dir}/${SOURCE_HOST}/${SOURCE_NAMESPACE}/${SOURCE_TYPE}/${PROVIDER_VERSION}/${OS}_${ARCH}/"

work_dir="${ROOT_DIR}/test-results/live-view-drift-${RUN_ID}"
if [[ -e "${work_dir}" ]]; then
  echo "Refusing to reuse existing test directory: ${work_dir}" >&2
  exit 1
fi
mkdir -p "${work_dir}"
cp "${ROOT_DIR}/test-fixtures/live-view-drift/main.tf" "${work_dir}/main.tf"
perl -0pi -e "s/version = \"= 0\\.1\\.0\"/version = \"= ${PROVIDER_VERSION}\"/" "${work_dir}/main.tf"

cat > "${work_dir}/terraform.tfvars" <<HCL
run_id = "${RUN_ID}"
HCL

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

database_name="tf_view_drift_${RUN_ID//-/_}"

cleanup() {
  local destroy_status=0
  if [[ "${KEEP_LIVE_FIXTURE}" != "1" && -d "${work_dir}/.terraform" ]]; then
    live_terraform_destroy "${cli_config}" "${TERRAFORM_BIN}" "${work_dir}" || destroy_status=$?
  fi
  if [[ "${KEEP_LIVE_FIXTURE}" != "1" ]]; then
    live_drop_database "${database_name}"
  fi
  return "${destroy_status}"
}
trap cleanup EXIT

echo "==> Live view drift smoke (${RUN_ID})"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" init -backend=false -input=false
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" validate
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false

schema_name="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -raw schema_name)"
table_name="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -raw table_name)"
view_name="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -raw view_name)"

go run "${ROOT_DIR}/internal/dev/mdexec" -database "${database_name}" -sql "CREATE OR REPLACE VIEW \"${database_name}\".\"${schema_name}\".\"${view_name}\" AS SELECT id FROM \"${database_name}\".\"${schema_name}\".\"${table_name}\" WHERE id > 10"

set +e
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" plan -detailed-exitcode -input=false
drift_plan_exit=$?
set -e

if [[ "${drift_plan_exit}" -ne 2 ]]; then
  echo "Expected Terraform to detect out-of-band view query drift with exit code 2, got ${drift_plan_exit}" >&2
  exit 1
fi

TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false

set +e
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" plan -detailed-exitcode -input=false
repair_plan_exit=$?
set -e

if [[ "${repair_plan_exit}" -ne 0 ]]; then
  if [[ "${repair_plan_exit}" -eq 2 ]]; then
    echo "Expected no-op plan after repairing view drift, but Terraform reported changes" >&2
  fi
  exit "${repair_plan_exit}"
fi

if [[ "${KEEP_LIVE_FIXTURE}" == "1" ]]; then
  trap - EXIT
  echo "Kept live fixture at ${work_dir}"
else
  trap - EXIT
  cleanup
fi
