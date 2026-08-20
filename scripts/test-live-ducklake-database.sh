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

work_dir="${ROOT_DIR}/test-results/live-ducklake-database-${RUN_ID}"
import_dir="${ROOT_DIR}/test-results/live-ducklake-database-imported-${RUN_ID}"
if [[ -e "${work_dir}" || -e "${import_dir}" ]]; then
  echo "Refusing to reuse existing test directory for run id: ${RUN_ID}" >&2
  exit 1
fi
mkdir -p "${work_dir}" "${import_dir}"
cp "${ROOT_DIR}/test-fixtures/live-ducklake-database/main.tf" "${work_dir}/main.tf"
cp "${ROOT_DIR}/test-fixtures/live-ducklake-database-imported/main.tf" "${import_dir}/main.tf"
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

cat > "${work_dir}/terraform.tfvars" <<HCL
run_id = "${RUN_ID}"
HCL

database_name="tf_ducklake_${RUN_ID//-/_}"

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

echo "==> Live DuckLake database smoke (${RUN_ID})"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" init -backend=false -input=false
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" validate
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false

if [[ "$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -raw database_type)" != "ducklake" ]]; then
  echo "Expected DuckLake database_type to read back as ducklake" >&2
  exit 1
fi
if [[ "$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -raw snapshot_retention_days)" != "7" ]]; then
  echo "Expected DuckLake snapshot_retention_days to read back as 7" >&2
  exit 1
fi

go run "${ROOT_DIR}/internal/dev/mdexec" -database "${database_name}" -sql "INSERT INTO \"${database_name}\".app.facts VALUES (1, 'ducklake terraform smoke')"
row_count="$(go run "${ROOT_DIR}/internal/dev/mdexec" -database "${database_name}" -scalar "SELECT count(*)::VARCHAR FROM \"${database_name}\".app.facts")"
if [[ "${row_count}" != "1" ]]; then
  echo "Expected one row in DuckLake smoke table, got ${row_count}" >&2
  exit 1
fi

cat > "${import_dir}/terraform.tfvars" <<HCL
database_name = "${database_name}"
HCL

TF_CLI_CONFIG_FILE="${import_dir}/terraformrc" "${TERRAFORM_BIN}" -chdir="${import_dir}" init -backend=false -input=false
TF_CLI_CONFIG_FILE="${import_dir}/terraformrc" "${TERRAFORM_BIN}" -chdir="${import_dir}" import -input=false motherduck_database.imported "${database_name}"

set +e
TF_CLI_CONFIG_FILE="${import_dir}/terraformrc" "${TERRAFORM_BIN}" -chdir="${import_dir}" plan -detailed-exitcode -input=false
import_plan_exit=$?
set -e

if [[ "${import_plan_exit}" -ne 0 ]]; then
  if [[ "${import_plan_exit}" -eq 2 ]]; then
    echo "Expected no-op plan after DuckLake database import, but Terraform reported changes" >&2
  fi
  exit "${import_plan_exit}"
fi

set +e
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" plan -detailed-exitcode -input=false
plan_exit=$?
set -e

if [[ "${plan_exit}" -ne 0 ]]; then
  if [[ "${plan_exit}" -eq 2 ]]; then
    echo "Expected no-op plan after DuckLake database apply, but Terraform reported changes" >&2
  fi
  exit "${plan_exit}"
fi

if [[ "${KEEP_LIVE_FIXTURE}" == "1" ]]; then
  trap - EXIT
  echo "Kept live fixture at ${work_dir}"
else
  trap - EXIT
  cleanup
fi
