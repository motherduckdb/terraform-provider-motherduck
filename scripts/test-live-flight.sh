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
RUN_FLIGHT="${MD_TF_ACC_ENABLE_FLIGHT_RUNS:-0}"
case "${RUN_FLIGHT}" in
  1 | true | TRUE)
    run_flight_hcl="true"
    ;;
  0 | false | FALSE)
    run_flight_hcl="false"
    ;;
  *)
    echo "MD_TF_ACC_ENABLE_FLIGHT_RUNS must be 0/1 or true/false" >&2
    exit 1
    ;;
esac

if [[ -z "${MOTHERDUCK_TOKEN:-}" ]]; then
  echo "MOTHERDUCK_TOKEN is required for Flight smoke tests" >&2
  exit 1
fi

flight_function_count="$(go run "${ROOT_DIR}/internal/dev/mdexec" -scalar "SELECT count(*)::VARCHAR FROM duckdb_functions() WHERE lower(function_name) = 'md_create_flight'")"
if [[ "${flight_function_count}" == "0" ]]; then
  if [[ "${MD_TF_ACC_REQUIRE_FLIGHTS:-0}" == "1" ]]; then
    echo "Expected MD_CREATE_FLIGHT to be available in this MotherDuck SQL session" >&2
    exit 1
  fi
  echo "Skipping live Flight smoke: MD_CREATE_FLIGHT is not available in this MotherDuck SQL session" >&2
  exit 0
fi

provider_dir="${PROVIDER_BIN_DIR:-${ROOT_DIR}/tools/provider-bin/${RUN_ID}}"
mirror_dir="${PROVIDER_MIRROR_DIR:-${ROOT_DIR}/tools/provider-mirror/${RUN_ID}}"
mkdir -p "${provider_dir}" "${mirror_dir}/${SOURCE_HOST}/${SOURCE_NAMESPACE}/${SOURCE_TYPE}/${PROVIDER_VERSION}/${OS}_${ARCH}"

provider_binary="${provider_dir}/terraform-provider-${SOURCE_TYPE}_v${PROVIDER_VERSION}"
GOOS="${OS}" GOARCH="${ARCH}" go build -o "${provider_binary}" "${ROOT_DIR}"
cp "${provider_binary}" "${mirror_dir}/${SOURCE_HOST}/${SOURCE_NAMESPACE}/${SOURCE_TYPE}/${PROVIDER_VERSION}/${OS}_${ARCH}/"

work_dir="${ROOT_DIR}/test-results/live-flight-${RUN_ID}"
if [[ -e "${work_dir}" ]]; then
  echo "Refusing to reuse existing test directory: ${work_dir}" >&2
  exit 1
fi
mkdir -p "${work_dir}"
cp "${ROOT_DIR}/test-fixtures/live-flight/main.tf" "${work_dir}/main.tf"
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

cleanup() {
  if [[ "${KEEP_LIVE_FIXTURE}" != "1" && -d "${work_dir}/.terraform" ]]; then
    TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" destroy -auto-approve -input=false
  fi
}
trap cleanup EXIT

write_vars() {
  local source_label="$1"
  cat > "${work_dir}/terraform.tfvars" <<HCL
run_id = "${RUN_ID}"
source_label = "${source_label}"
run_flight = ${run_flight_hcl}
HCL
}

echo "==> Live Flight smoke (${RUN_ID})"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" init -backend=false -input=false
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" validate

write_vars "initial flight content"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false

write_vars "updated flight content"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false

if [[ "$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -raw max_runtime_sec)" != "300" ]]; then
  echo "Expected Flight max_runtime_sec to refresh as 300" >&2
  exit 1
fi

set +e
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" plan -detailed-exitcode -input=false
plan_exit=$?
set -e

if [[ "${plan_exit}" -ne 0 ]]; then
  if [[ "${plan_exit}" -eq 2 && "${run_flight_hcl}" == "true" ]]; then
    echo "Run-enabled Flight smoke reported a non-empty follow-up plan; Flight run rows are dynamic while runs transition status."
  else
    if [[ "${plan_exit}" -eq 2 ]]; then
      echo "Expected no-op plan after Flight update, but Terraform reported changes" >&2
    fi
    exit "${plan_exit}"
  fi
fi

if [[ "${KEEP_LIVE_FIXTURE}" == "1" ]]; then
  trap - EXIT
  echo "Kept live fixture at ${work_dir}"
else
  trap - EXIT
  cleanup
fi
