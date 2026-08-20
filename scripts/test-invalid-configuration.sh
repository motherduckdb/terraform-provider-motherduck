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

provider_dir="${PROVIDER_BIN_DIR:-${ROOT_DIR}/tools/provider-bin/${RUN_ID}}"
mirror_dir="${PROVIDER_MIRROR_DIR:-${ROOT_DIR}/tools/provider-mirror/${RUN_ID}}"
mkdir -p "${provider_dir}" "${mirror_dir}/${SOURCE_HOST}/${SOURCE_NAMESPACE}/${SOURCE_TYPE}/${PROVIDER_VERSION}/${OS}_${ARCH}"

provider_binary="${provider_dir}/terraform-provider-${SOURCE_TYPE}_v${PROVIDER_VERSION}"
GOOS="${OS}" GOARCH="${ARCH}" go build -o "${provider_binary}" "${ROOT_DIR}"
cp "${provider_binary}" "${mirror_dir}/${SOURCE_HOST}/${SOURCE_NAMESPACE}/${SOURCE_TYPE}/${PROVIDER_VERSION}/${OS}_${ARCH}/"

work_dir="${ROOT_DIR}/test-results/invalid-configuration-${RUN_ID}"
if [[ -e "${work_dir}" ]]; then
  echo "Refusing to reuse existing test directory: ${work_dir}" >&2
  exit 1
fi
mkdir -p "${work_dir}"
cp "${ROOT_DIR}/test-fixtures/invalid-configuration/main.tf" "${work_dir}/main.tf"
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

echo "==> Invalid configuration validation smoke (${RUN_ID})"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" init -backend=false -input=false

set +e
validate_output="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" validate -no-color 2>&1)"
validate_exit=$?
set -e

if [[ "${validate_exit}" -eq 0 ]]; then
  echo "Expected terraform validate to fail for invalid configuration" >&2
  exit 1
fi
if [[ "${validate_output}" != *"Invalid MotherDuck SQL identifier"* ]]; then
  echo "Expected dotted identifier diagnostic, got:" >&2
  printf '%s\n' "${validate_output}" >&2
  exit 1
fi
if [[ "${validate_output}" != *"leading or trailing whitespace"* ]]; then
  echo "Expected whitespace-wrapped identifier diagnostic, got:" >&2
  printf '%s\n' "${validate_output}" >&2
  exit 1
fi
if [[ "${validate_output}" != *"Invalid MotherDuck database type"* ]]; then
  echo "Expected transient database_type diagnostic, got:" >&2
  printf '%s\n' "${validate_output}" >&2
  exit 1
fi
if [[ "${validate_output}" != *"Invalid MotherDuck snapshot retention days"* ]]; then
  echo "Expected snapshot retention diagnostic, got:" >&2
  printf '%s\n' "${validate_output}" >&2
  exit 1
fi
if [[ "${validate_output}" != *"lowercase canonical"* || "${validate_output}" != *"lowercase bare SQL option word"* ]]; then
  echo "Expected lowercase canonical value diagnostics, got:" >&2
  printf '%s\n' "${validate_output}" >&2
  exit 1
fi
if [[ "${validate_output}" != *"Invalid MotherDuck data source limit"* || "${validate_output}" != *"Invalid MotherDuck data source offset"* || "${validate_output}" != *"Invalid MotherDuck data source run number"* ]]; then
  echo "Expected data source numeric bound diagnostics, got:" >&2
  printf '%s\n' "${validate_output}" >&2
  exit 1
fi
if [[ "${validate_output}" != *"Invalid MotherDuck REST username"* || "${validate_output}" != *"Invalid MotherDuck UUID"* || "${validate_output}" != *"Invalid MotherDuck Dive embed session hint"* ]]; then
  echo "Expected REST data source input diagnostics, got:" >&2
  printf '%s\n' "${validate_output}" >&2
  exit 1
fi
if [[ "${validate_output}" != *"must not include leading or trailing whitespace"* ]]; then
  echo "Expected UUID whitespace diagnostic, got:" >&2
  printf '%s\n' "${validate_output}" >&2
  exit 1
fi
if [[ "${validate_output}" != *"Invalid MotherDuck Flight run wait status"* || "${validate_output}" != *"Invalid MotherDuck Flight run poll interval"* || "${validate_output}" != *"Invalid MotherDuck Flight run timeout"* ]]; then
  echo "Expected Flight run wait option diagnostics, got:" >&2
  printf '%s\n' "${validate_output}" >&2
  exit 1
fi
if [[ "${validate_output}" != *"Invalid MotherDuck Flight config"* || "${validate_output}" != *"reserved and cannot be set"* || "${validate_output}" != *'must not contain "=".'* ]]; then
  echo "Expected Flight config diagnostics, got:" >&2
  printf '%s\n' "${validate_output}" >&2
  exit 1
fi

if [[ "${validate_output}" != *"data_path"* ]]; then
  echo "Expected DuckLake data_path diagnostic, got:" >&2
  printf '%s\n' "${validate_output}" >&2
  exit 1
fi
if [[ "${validate_output}" != *"encrypted"* ]]; then
  echo "Expected DuckLake encrypted diagnostic, got:" >&2
  printf '%s\n' "${validate_output}" >&2
  exit 1
fi

provider_work_dir="${ROOT_DIR}/test-results/invalid-provider-configuration-${RUN_ID}"
if [[ -e "${provider_work_dir}" ]]; then
  echo "Refusing to reuse existing test directory: ${provider_work_dir}" >&2
  exit 1
fi
mkdir -p "${provider_work_dir}"
cp "${ROOT_DIR}/test-fixtures/invalid-provider-configuration/main.tf" "${provider_work_dir}/main.tf"
perl -0pi -e "s/version = \"= 0\\.1\\.0\"/version = \"= ${PROVIDER_VERSION}\"/" "${provider_work_dir}/main.tf"

TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${provider_work_dir}" init -backend=false -input=false

set +e
provider_validate_output="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${provider_work_dir}" validate -no-color 2>&1)"
provider_validate_exit=$?
set -e

if [[ "${provider_validate_exit}" -eq 0 ]]; then
  echo "Expected terraform validate to fail for invalid provider configuration" >&2
  exit 1
fi
if [[ "${provider_validate_output}" != *"Invalid MotherDuck API base URL"* ]]; then
  echo "Expected API base URL diagnostic, got:" >&2
  printf '%s\n' "${provider_validate_output}" >&2
  exit 1
fi
if [[ "${provider_validate_output}" != *"must not include username or password credentials"* ]]; then
  echo "Expected API base URL credential diagnostic, got:" >&2
  printf '%s\n' "${provider_validate_output}" >&2
  exit 1
fi
if [[ "${provider_validate_output}" != *"Invalid MotherDuck attach mode"* ]]; then
  echo "Expected attach mode diagnostic, got:" >&2
  printf '%s\n' "${provider_validate_output}" >&2
  exit 1
fi

provider_shape_work_dir="${ROOT_DIR}/test-results/invalid-provider-configuration-shape-${RUN_ID}"
if [[ -e "${provider_shape_work_dir}" ]]; then
  echo "Refusing to reuse existing test directory: ${provider_shape_work_dir}" >&2
  exit 1
fi
mkdir -p "${provider_shape_work_dir}"
cp "${ROOT_DIR}/test-fixtures/invalid-provider-configuration-shape/main.tf" "${provider_shape_work_dir}/main.tf"
perl -0pi -e "s/version = \"= 0\\.1\\.0\"/version = \"= ${PROVIDER_VERSION}\"/" "${provider_shape_work_dir}/main.tf"

TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${provider_shape_work_dir}" init -backend=false -input=false

set +e
provider_shape_validate_output="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${provider_shape_work_dir}" validate -no-color 2>&1)"
provider_shape_validate_exit=$?
set -e

if [[ "${provider_shape_validate_exit}" -eq 0 ]]; then
  echo "Expected terraform validate to fail for invalid provider shape configuration" >&2
  exit 1
fi
if [[ "${provider_shape_validate_output}" != *"must not include a query string or fragment"* ]]; then
  echo "Expected API base URL query/fragment diagnostic, got:" >&2
  printf '%s\n' "${provider_shape_validate_output}" >&2
  exit 1
fi
if [[ "${provider_shape_validate_output}" != *"Invalid MotherDuck attach mode"* || "${provider_shape_validate_output}" != *"must not include leading or trailing whitespace"* ]]; then
  echo "Expected attach mode whitespace diagnostic, got:" >&2
  printf '%s\n' "${provider_shape_validate_output}" >&2
  exit 1
fi
if [[ "${validate_output}" != *"transient"* || "${validate_output}" != *"ducklake"* ]]; then
  echo "Expected transient DuckLake diagnostic, got:" >&2
  printf '%s\n' "${validate_output}" >&2
  exit 1
fi
if [[ "${validate_output}" != *"data_path"* || "${validate_output}" != *"non-empty DuckLake storage path"* ]]; then
  echo "Expected blank DuckLake data_path diagnostic, got:" >&2
  printf '%s\n' "${validate_output}" >&2
  exit 1
fi
if [[ "${validate_output}" != *"Invalid MotherDuck table columns"* ]]; then
  echo "Expected empty table columns diagnostic, got:" >&2
  printf '%s\n' "${validate_output}" >&2
  exit 1
fi
if [[ "${validate_output}" != *"Invalid MotherDuck table column type"* ]]; then
  echo "Expected table column type diagnostic, got:" >&2
  printf '%s\n' "${validate_output}" >&2
  exit 1
fi
if [[ "${validate_output}" != *"Invalid MotherDuck SQL option"* ]]; then
  echo "Expected SQL option diagnostic, got:" >&2
  printf '%s\n' "${validate_output}" >&2
  exit 1
fi
if [[ "${validate_output}" != *"Invalid MotherDuck secret parameter"* ]]; then
  echo "Expected secret parameter diagnostic, got:" >&2
  printf '%s\n' "${validate_output}" >&2
  exit 1
fi
if [[ "${validate_output}" != *"Invalid MotherDuck secret SQL"* ]]; then
  echo "Expected secret SQL diagnostic, got:" >&2
  printf '%s\n' "${validate_output}" >&2
  exit 1
fi
if [[ "${validate_output}" != *"Invalid MotherDuck share access"* || "${validate_output}" != *"Invalid MotherDuck share visibility"* || "${validate_output}" != *"Invalid MotherDuck share update mode"* ]]; then
  echo "Expected share option diagnostics, got:" >&2
  printf '%s\n' "${validate_output}" >&2
  exit 1
fi
if [[ "${validate_output}" != *"Invalid MotherDuck share grant username"* ]]; then
  echo "Expected share grant username diagnostic, got:" >&2
  printf '%s\n' "${validate_output}" >&2
  exit 1
fi
if [[ "${validate_output}" != *"Invalid MotherDuck service account username"* ]]; then
  echo "Expected service account username diagnostic, got:" >&2
  printf '%s\n' "${validate_output}" >&2
  exit 1
fi
if [[ "${validate_output}" != *"Invalid MotherDuck access token name"* || "${validate_output}" != *"Invalid MotherDuck access token TTL"* || "${validate_output}" != *"Invalid MotherDuck access token type"* ]]; then
  echo "Expected access token diagnostics, got:" >&2
  printf '%s\n' "${validate_output}" >&2
  exit 1
fi
if [[ "${validate_output}" != *"Invalid MotherDuck Duckling instance size"* || "${validate_output}" != *"Invalid MotherDuck Duckling cooldown seconds"* || "${validate_output}" != *"Invalid MotherDuck read-scaling flock size"* ]]; then
  echo "Expected Duckling config diagnostics, got:" >&2
  printf '%s\n' "${validate_output}" >&2
  exit 1
fi

deferred_work_dir="${ROOT_DIR}/test-results/deferred-configuration-${RUN_ID}"
if [[ -e "${deferred_work_dir}" ]]; then
  echo "Refusing to reuse existing test directory: ${deferred_work_dir}" >&2
  exit 1
fi
mkdir -p "${deferred_work_dir}"
cp "${ROOT_DIR}/test-fixtures/deferred-configuration/main.tf" "${deferred_work_dir}/main.tf"
perl -0pi -e "s/version = \"= 0\\.1\\.0\"/version = \"= ${PROVIDER_VERSION}\"/" "${deferred_work_dir}/main.tf"

TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${deferred_work_dir}" init -backend=false -input=false
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${deferred_work_dir}" validate -no-color
