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
EXISTING_GRANTEE_USERNAME="${MD_TF_ACC_SHARE_GRANTEE_USERNAME:-}"

if [[ -z "${MOTHERDUCK_TOKEN:-}" ]]; then
  echo "MOTHERDUCK_TOKEN is required for SQL smoke tests" >&2
  exit 1
fi

if [[ "${EXISTING_GRANTEE_USERNAME}" == *$'\n'* || "${EXISTING_GRANTEE_USERNAME}" == *\"* ]]; then
  echo "MD_TF_ACC_SHARE_GRANTEE_USERNAME must not contain newlines or double quotes" >&2
  exit 1
fi

fixture_path="${ROOT_DIR}/test-fixtures/live-share-grant-existing-principal/main.tf"
using_existing_grantee=1

if [[ -n "${EXISTING_GRANTEE_USERNAME}" ]]; then
  :
elif [[ -n "${MOTHERDUCK_ADMIN_TOKEN:-}" ]]; then
  source "${ROOT_DIR}/scripts/lib/live-rest.sh"
  set +e
  preflight_rest_admin
  preflight_exit=$?
  set -e
  if [[ "${preflight_exit}" -eq 0 ]]; then
    fixture_path="${ROOT_DIR}/test-fixtures/live-share-grant-drift/main.tf"
    using_existing_grantee=0
  elif [[ "${preflight_exit}" -eq 42 ]]; then
    exit 0
  else
    exit "${preflight_exit}"
  fi
else
  echo "Skipping live share grant drift smoke: set MOTHERDUCK_ADMIN_TOKEN for a temporary service account or MD_TF_ACC_SHARE_GRANTEE_USERNAME for an existing grantable principal." >&2
  exit 0
fi

suffix="${RUN_ID//-/_}"
if [[ "${using_existing_grantee}" == "1" ]]; then
  database_name="tf_grant_existing_${suffix}"
  share_name="tf_grant_existing_share_${suffix}"
else
  database_name="tf_grant_drift_${suffix}"
  share_name="tf_grant_drift_share_${suffix}"
fi

print_preflight_error() {
  local grantee_username="$1"
  local error_file="$2"
  local redacted_file="${error_file}.redacted"

  if [[ ! -s "${error_file}" ]]; then
    return 0
  fi

  GRANTEE_USERNAME="${grantee_username}" perl -0pe '
    BEGIN { $grantee = $ENV{"GRANTEE_USERNAME"} // ""; }
    s/\Q$grantee\E/[redacted grantee]/g if length $grantee;
  ' "${error_file}" >"${redacted_file}"
  mv "${redacted_file}" "${error_file}"
  cat "${error_file}" >&2
}

preflight_existing_share_grantee() {
  local grantee_username="$1"
  local preflight_database="$2"
  local preflight_share="$3"
  local preflight_table="tf_grant_probe"
  local error_file="${ROOT_DIR}/test-results/share-grant-preflight-${RUN_ID}.log"

  live_drop_share "${preflight_share}"
  live_drop_database "${preflight_database}"

  if ! go run "${ROOT_DIR}/internal/dev/mdexec" -sql "CREATE DATABASE $(sql_identifier "${preflight_database}")" >"${error_file}" 2>&1; then
    echo "Unable to create temporary database for share-grant preflight." >&2
    print_preflight_error "${grantee_username}" "${error_file}"
    live_drop_database "${preflight_database}"
    return 1
  fi

  if ! go run "${ROOT_DIR}/internal/dev/mdexec" -database "${preflight_database}" -pre "USE $(sql_identifier "${preflight_database}")" -sql "CREATE TABLE $(sql_identifier "${preflight_table}") (id INTEGER)" >"${error_file}" 2>&1; then
    echo "Unable to create temporary table for share-grant preflight." >&2
    print_preflight_error "${grantee_username}" "${error_file}"
    live_drop_database "${preflight_database}"
    return 1
  fi

  if ! go run "${ROOT_DIR}/internal/dev/mdexec" -database "${preflight_database}" -sql "CREATE SHARE $(sql_identifier "${preflight_share}") FROM $(sql_identifier "${preflight_database}") (ACCESS RESTRICTED, VISIBILITY HIDDEN)" >"${error_file}" 2>&1; then
    echo "Unable to create temporary share for share-grant preflight." >&2
    print_preflight_error "${grantee_username}" "${error_file}"
    live_drop_share "${preflight_share}"
    live_drop_database "${preflight_database}"
    return 1
  fi

  if ! go run "${ROOT_DIR}/internal/dev/mdexec" -sql "GRANT READ ON SHARE $(sql_identifier "${preflight_share}") TO $(sql_identifier "${grantee_username}")" >"${error_file}" 2>&1; then
    echo "MD_TF_ACC_SHARE_GRANTEE_USERNAME is not accepted by GRANT READ ON SHARE." >&2
    echo "Set it to a grantable MotherDuck user or service-account username; the motherduck_current_user value and PAT metadata are often not grantable." >&2
    print_preflight_error "${grantee_username}" "${error_file}"
    live_drop_share "${preflight_share}"
    live_drop_database "${preflight_database}"
    return 1
  fi

  go run "${ROOT_DIR}/internal/dev/mdexec" -sql "REVOKE READ ON SHARE $(sql_identifier "${preflight_share}") FROM $(sql_identifier "${grantee_username}")" >/dev/null 2>&1 || true
  live_drop_share "${preflight_share}"
  live_drop_database "${preflight_database}"
}

if [[ "${using_existing_grantee}" == "1" ]]; then
  preflight_existing_share_grantee "${EXISTING_GRANTEE_USERNAME}" "tf_grant_preflight_${suffix}" "tf_grant_preflight_share_${suffix}"
fi

provider_dir="${PROVIDER_BIN_DIR:-${ROOT_DIR}/tools/provider-bin/${RUN_ID}}"
mirror_dir="${PROVIDER_MIRROR_DIR:-${ROOT_DIR}/tools/provider-mirror/${RUN_ID}}"
mkdir -p "${provider_dir}" "${mirror_dir}/${SOURCE_HOST}/${SOURCE_NAMESPACE}/${SOURCE_TYPE}/${PROVIDER_VERSION}/${OS}_${ARCH}"

provider_binary="${provider_dir}/terraform-provider-${SOURCE_TYPE}_v${PROVIDER_VERSION}"
GOOS="${OS}" GOARCH="${ARCH}" go build -o "${provider_binary}" "${ROOT_DIR}"
cp "${provider_binary}" "${mirror_dir}/${SOURCE_HOST}/${SOURCE_NAMESPACE}/${SOURCE_TYPE}/${PROVIDER_VERSION}/${OS}_${ARCH}/"

work_dir="${ROOT_DIR}/test-results/live-share-grant-drift-${RUN_ID}"
if [[ -e "${work_dir}" ]]; then
  echo "Refusing to reuse existing test directory: ${work_dir}" >&2
  exit 1
fi
mkdir -p "${work_dir}"
cp "${fixture_path}" "${work_dir}/main.tf"
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

run_terraform() {
  if [[ "${using_existing_grantee}" == "1" ]]; then
    TF_VAR_grantee_username="${EXISTING_GRANTEE_USERNAME}" TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" "$@"
  else
    TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" "$@"
  fi
}

cleanup() {
  local destroy_status=0
  if [[ "${KEEP_LIVE_FIXTURE}" != "1" && -d "${work_dir}/.terraform" ]]; then
    run_terraform destroy -auto-approve -input=false || destroy_status=$?
  fi
  if [[ "${KEEP_LIVE_FIXTURE}" != "1" ]]; then
    live_drop_share "${share_name}"
    live_drop_database "${database_name}"
  fi
  return "${destroy_status}"
}
trap cleanup EXIT

echo "==> Live share grant drift smoke (${RUN_ID})"
run_terraform init -backend=false -input=false
run_terraform validate
run_terraform apply -auto-approve -input=false

share_name="$(run_terraform output -raw share_name)"
if [[ "${using_existing_grantee}" == "1" ]]; then
  reader_username="${EXISTING_GRANTEE_USERNAME}"
else
  reader_username="$(run_terraform output -raw reader_username)"
fi
go run "${ROOT_DIR}/internal/dev/mdexec" -sql "REVOKE READ ON SHARE $(sql_identifier "${share_name}") FROM $(sql_identifier "${reader_username}")"

set +e
run_terraform plan -detailed-exitcode -input=false
plan_exit=$?
set -e

if [[ "${plan_exit}" -ne 2 ]]; then
  echo "Expected Terraform to detect revoked share grant drift with exit code 2, got ${plan_exit}" >&2
  exit 1
fi

run_terraform apply -auto-approve -input=false

set +e
run_terraform plan -detailed-exitcode -input=false
repair_plan_exit=$?
set -e

if [[ "${repair_plan_exit}" -ne 0 ]]; then
  if [[ "${repair_plan_exit}" -eq 2 ]]; then
    echo "Expected no-op plan after repairing share grant drift, but Terraform reported changes" >&2
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
