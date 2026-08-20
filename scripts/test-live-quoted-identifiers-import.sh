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

source_dir="${ROOT_DIR}/test-results/live-quoted-identifiers-import-source-${RUN_ID}"
import_dir="${ROOT_DIR}/test-results/live-quoted-identifiers-import-imported-${RUN_ID}"
if [[ -e "${source_dir}" || -e "${import_dir}" ]]; then
  echo "Refusing to reuse existing test directory for run id: ${RUN_ID}" >&2
  exit 1
fi
mkdir -p "${source_dir}" "${import_dir}"
cp "${ROOT_DIR}/test-fixtures/live-quoted-identifiers/main.tf" "${source_dir}/main.tf"
cp "${ROOT_DIR}/test-fixtures/live-quoted-identifiers-imported/main.tf" "${import_dir}/main.tf"
perl -0pi -e "s/version = \"= 0\\.1\\.0\"/version = \"= ${PROVIDER_VERSION}\"/" "${source_dir}/main.tf" "${import_dir}/main.tf"

cli_config="${source_dir}/terraformrc"
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

cat > "${source_dir}/terraform.tfvars" <<HCL
run_id = "${RUN_ID}"
HCL

database_name=""
share_name=""
secret_name=""

cleanup() {
  local destroy_status=0
  if [[ "${KEEP_LIVE_FIXTURE}" != "1" && -d "${source_dir}/.terraform" ]]; then
    TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${source_dir}" destroy -auto-approve -input=false || destroy_status=$?
  fi
  if [[ -n "${share_name}" ]]; then
    go run "${ROOT_DIR}/internal/dev/mdexec" -sql "DROP SHARE IF EXISTS \"${share_name}\"" >/dev/null 2>&1 || true
  fi
  if [[ -n "${secret_name}" ]]; then
    go run "${ROOT_DIR}/internal/dev/mdexec" -sql "DROP SECRET IF EXISTS \"${secret_name}\" FROM motherduck" >/dev/null 2>&1 || true
  fi
  if [[ -n "${database_name}" ]]; then
    go run "${ROOT_DIR}/internal/dev/mdexec" -sql "DROP DATABASE IF EXISTS \"${database_name}\" CASCADE" >/dev/null 2>&1 || true
  fi
  return "${destroy_status}"
}
trap cleanup EXIT

echo "==> Live quoted identifier import smoke (${RUN_ID})"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${source_dir}" init -backend=false -input=false
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${source_dir}" validate
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${source_dir}" apply -auto-approve -input=false

suffix="${RUN_ID//-/_}"
database_name="tf qident ${suffix}"
schema_name="app schema"
table_name="facts table"
view_name="facts view"
share_name="tf qident share ${suffix}"
snapshot_name="tf qident snapshot ${suffix}"
secret_name="tf qident secret ${suffix}"

view_query="$(go run "${ROOT_DIR}/internal/dev/mdexec" -database "${database_name}" -scalar "SELECT regexp_replace(substr(view_definition, strpos(view_definition, ' AS ') + 4), ';$', '') FROM information_schema.views WHERE table_catalog = '${database_name}' AND table_schema = '${schema_name}' AND table_name = '${view_name}'")"

cat > "${import_dir}/terraform.tfvars" <<HCL
database_name = "${database_name}"
schema_name = "${schema_name}"
table_name = "${table_name}"
view_name = "${view_name}"
view_query = <<SQL
${view_query}
SQL
share_name = "${share_name}"
snapshot_name = "${snapshot_name}"
secret_name = "${secret_name}"
HCL

TF_CLI_CONFIG_FILE="${import_dir}/terraformrc" "${TERRAFORM_BIN}" -chdir="${import_dir}" init -backend=false -input=false
TF_CLI_CONFIG_FILE="${import_dir}/terraformrc" "${TERRAFORM_BIN}" -chdir="${import_dir}" import -input=false motherduck_database.imported "${database_name}"
TF_CLI_CONFIG_FILE="${import_dir}/terraformrc" "${TERRAFORM_BIN}" -chdir="${import_dir}" import -input=false motherduck_schema.imported "${database_name}.${schema_name}"
TF_CLI_CONFIG_FILE="${import_dir}/terraformrc" "${TERRAFORM_BIN}" -chdir="${import_dir}" import -input=false motherduck_table.imported "${database_name}.${schema_name}.${table_name}"
TF_CLI_CONFIG_FILE="${import_dir}/terraformrc" "${TERRAFORM_BIN}" -chdir="${import_dir}" import -input=false motherduck_view.imported "${database_name}.${schema_name}.${view_name}"
TF_CLI_CONFIG_FILE="${import_dir}/terraformrc" "${TERRAFORM_BIN}" -chdir="${import_dir}" import -input=false motherduck_share.imported "${share_name}"
TF_CLI_CONFIG_FILE="${import_dir}/terraformrc" "${TERRAFORM_BIN}" -chdir="${import_dir}" import -input=false motherduck_snapshot.imported "${database_name}.${snapshot_name}"
TF_CLI_CONFIG_FILE="${import_dir}/terraformrc" "${TERRAFORM_BIN}" -chdir="${import_dir}" import -input=false motherduck_secret.imported "${secret_name}"

set +e
TF_CLI_CONFIG_FILE="${import_dir}/terraformrc" "${TERRAFORM_BIN}" -chdir="${import_dir}" plan -detailed-exitcode -input=false
plan_exit=$?
set -e

if [[ "${plan_exit}" -ne 0 ]]; then
  if [[ "${plan_exit}" -eq 2 ]]; then
    echo "Expected no-op plan after quoted identifier import, but Terraform reported changes" >&2
  fi
  exit "${plan_exit}"
fi

if [[ "${KEEP_LIVE_FIXTURE}" == "1" ]]; then
  trap - EXIT
  echo "Kept live fixture at ${source_dir}"
else
  trap - EXIT
  cleanup
fi
