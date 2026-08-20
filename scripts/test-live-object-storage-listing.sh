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
OBJECT_STORAGE_PATH="${MD_TF_ACC_OBJECT_STORAGE_PATH:-s3://us-prd-motherduck-open-datasets/}"
BUCKET_SECRET_NAME="${MD_TF_ACC_BUCKET_SECRET_NAME:-__default_s3}"

if [[ -z "${MOTHERDUCK_TOKEN:-}" ]]; then
  echo "MOTHERDUCK_TOKEN is required for SQL smoke tests" >&2
  exit 1
fi

if [[ "${OBJECT_STORAGE_PATH}" == *$'\n'* || "${OBJECT_STORAGE_PATH}" == *'"'* ]]; then
  echo "MD_TF_ACC_OBJECT_STORAGE_PATH must not contain newlines or double quotes" >&2
  exit 1
fi

if [[ ! "${BUCKET_SECRET_NAME}" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
  echo "MD_TF_ACC_BUCKET_SECRET_NAME must be a simple SQL identifier" >&2
  exit 1
fi

available_count="$(go run "${ROOT_DIR}/internal/dev/mdexec" -pre "INSTALL httpfs; LOAD httpfs" -scalar "SELECT count(*)::VARCHAR FROM duckdb_functions() WHERE lower(function_name) IN ('md_list_files', 'md_list_buckets_for_secret')")"
if [[ "${available_count}" != "2" ]]; then
  if [[ "${MD_TF_ACC_REQUIRE_OBJECT_STORAGE_LISTING:-0}" == "1" ]]; then
    echo "Expected md_list_files and md_list_buckets_for_secret to be available, found ${available_count}/2" >&2
    exit 1
  fi
  echo "Skipping object-storage listing smoke: md_list_files/md_list_buckets_for_secret are not both exposed in this MotherDuck session."
  exit 0
fi

bucket_secret_count="$(go run "${ROOT_DIR}/internal/dev/mdexec" -pre "INSTALL httpfs; LOAD httpfs" -scalar "SELECT count(*)::VARCHAR FROM duckdb_secrets() WHERE name = '${BUCKET_SECRET_NAME}' AND lower(type) IN ('s3', 'aws')")"
include_bucket_listing=false
if [[ "${bucket_secret_count}" == "1" ]]; then
  include_bucket_listing=true
fi

provider_dir="${PROVIDER_BIN_DIR:-${ROOT_DIR}/tools/provider-bin/${RUN_ID}}"
mirror_dir="${PROVIDER_MIRROR_DIR:-${ROOT_DIR}/tools/provider-mirror/${RUN_ID}}"
mkdir -p "${provider_dir}" "${mirror_dir}/${SOURCE_HOST}/${SOURCE_NAMESPACE}/${SOURCE_TYPE}/${PROVIDER_VERSION}/${OS}_${ARCH}"

provider_binary="${provider_dir}/terraform-provider-${SOURCE_TYPE}_v${PROVIDER_VERSION}"
GOOS="${OS}" GOARCH="${ARCH}" go build -o "${provider_binary}" "${ROOT_DIR}"
cp "${provider_binary}" "${mirror_dir}/${SOURCE_HOST}/${SOURCE_NAMESPACE}/${SOURCE_TYPE}/${PROVIDER_VERSION}/${OS}_${ARCH}/"

work_dir="${ROOT_DIR}/test-results/live-object-storage-listing-${RUN_ID}"
if [[ -e "${work_dir}" ]]; then
  echo "Refusing to reuse existing test directory: ${work_dir}" >&2
  exit 1
fi
mkdir -p "${work_dir}"
cp "${ROOT_DIR}/test-fixtures/live-object-storage-listing/main.tf" "${work_dir}/main.tf"
perl -0pi -e "s/version = \"= 0\\.1\\.0\"/version = \"= ${PROVIDER_VERSION}\"/" "${work_dir}/main.tf"

cat > "${work_dir}/terraform.tfvars" <<HCL
path = "${OBJECT_STORAGE_PATH}"
include_bucket_listing = ${include_bucket_listing}
bucket_secret_name = "${BUCKET_SECRET_NAME}"
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

echo "==> Live object-storage listing smoke (${RUN_ID})"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" init -backend=false -input=false
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" validate
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false

set +e
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" plan -detailed-exitcode -input=false
plan_exit=$?
set -e

if [[ "${plan_exit}" -ne 0 ]]; then
  if [[ "${plan_exit}" -eq 2 ]]; then
    echo "Expected no-op plan after object-storage listing apply, but Terraform reported changes" >&2
  fi
  exit "${plan_exit}"
fi
