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
TERRAFORM_BIN="${TERRAFORM_BIN:-terraform}"
source "${ROOT_DIR}/scripts/lib/live-common.sh"
require_safe_run_id "${RUN_ID}"

provider_dir="${PROVIDER_BIN_DIR:-${ROOT_DIR}/tools/provider-bin/${RUN_ID}}"
mirror_dir="${PROVIDER_MIRROR_DIR:-${ROOT_DIR}/tools/provider-mirror/${RUN_ID}}"
mkdir -p "${provider_dir}" "${mirror_dir}/${SOURCE_HOST}/${SOURCE_NAMESPACE}/${SOURCE_TYPE}/${PROVIDER_VERSION}/${OS}_${ARCH}"

provider_binary="${provider_dir}/terraform-provider-${SOURCE_TYPE}_v${PROVIDER_VERSION}"
GOOS="${OS}" GOARCH="${ARCH}" go build -o "${provider_binary}" "${ROOT_DIR}"
cp "${provider_binary}" "${mirror_dir}/${SOURCE_HOST}/${SOURCE_NAMESPACE}/${SOURCE_TYPE}/${PROVIDER_VERSION}/${OS}_${ARCH}/"

work_dir="${ROOT_DIR}/test-results/invalid-import-${RUN_ID}"
if [[ -e "${work_dir}" ]]; then
  echo "Refusing to reuse existing test directory: ${work_dir}" >&2
  exit 1
fi
mkdir -p "${work_dir}"
cp "${ROOT_DIR}/test-fixtures/invalid-import/main.tf" "${work_dir}/main.tf"
perl -0pi -e "s/version = \"= 0\\.1\\.0\"/version = \"= ${PROVIDER_VERSION}\"/" "${work_dir}/main.tf"

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

echo "==> Invalid import diagnostics smoke (${RUN_ID})"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" init -backend=false -input=false >/dev/null
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" validate >/dev/null

run_invalid_import() {
  local address="$1"
  local import_id="$2"
  local expected="$3"

  set +e
  import_output="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" import -input=false -no-color "${address}" "${import_id}" 2>&1)"
  import_exit=$?
  set -e

  if [[ "${import_exit}" -eq 0 ]]; then
    echo "Expected ${address} import with ID ${import_id} to fail" >&2
    exit 1
  fi
  if [[ "${import_output}" != *"Invalid import ID"* && "${import_output}" != *"Invalid MotherDuck UUID"* ]] || [[ "${import_output}" != *"${expected}"* ]]; then
    echo "Expected invalid import diagnostic for ${address} to include ${expected}, got:" >&2
    printf '%s\n' "${import_output}" >&2
    exit 1
  fi
}

run_invalid_import "motherduck_database.database" "bad.name" "must not contain dots"
run_invalid_import "motherduck_share.share" " bad_share" "SQL resource name segments"
run_invalid_import "motherduck_secret.secret" "bad_secret " "SQL resource name segments"
run_invalid_import "motherduck_schema.schema" "valid_database. bad_schema" "SQL resource name segments"
run_invalid_import "motherduck_table.table" "valid_database..valid_table" "non-empty"
run_invalid_import "motherduck_share_grant.grant" "bad.share/valid_user" "must not contain dots"
run_invalid_import "motherduck_share_grant.grant" "valid_share/ valid_user" "Share grant username"
run_invalid_import "motherduck_access_token.token" "valid_user/ bad_token_id" "access token ID import segments"
run_invalid_import "motherduck_dive.dive" " 123e4567-e89b-42d3-a456-426614174000" "must not include leading or trailing whitespace"
run_invalid_import "motherduck_flight.flight" "not-a-uuid" "must be a UUID"
