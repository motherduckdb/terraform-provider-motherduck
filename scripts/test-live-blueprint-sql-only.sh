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

work_dir="${ROOT_DIR}/test-results/live-blueprint-sql-only-${RUN_ID}"
if [[ -e "${work_dir}" ]]; then
  echo "Refusing to reuse existing test directory: ${work_dir}" >&2
  exit 1
fi
mkdir -p "${work_dir}"
cp "${ROOT_DIR}/test-fixtures/live-blueprint-sql-only/main.tf" "${work_dir}/main.tf"
perl -0pi -e "s/version = \"= 0\\.1\\.0\"/version = \"= ${PROVIDER_VERSION}\"/" "${work_dir}/main.tf"

cat > "${work_dir}/terraform.tfvars" <<HCL
run_id = "${RUN_ID}"
tenants = {
  alpha = {
    display_name            = "Alpha Tenant"
    slug                    = "Alpha-One!"
    snapshot_retention_days = 1
  }
  beta = {
    display_name            = "Beta Tenant"
    slug                    = "Beta Team"
    snapshot_retention_days = 1
  }
}
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

suffix="${RUN_ID//-/_}"
database_alpha="tf_blueprint_${suffix}_alpha_one_"
database_beta="tf_blueprint_${suffix}_beta_team"
share_alpha="tf_blueprint_share_${suffix}_alpha_one_"
share_beta="tf_blueprint_share_${suffix}_beta_team"

cleanup() {
  local destroy_status=0
  if [[ "${KEEP_LIVE_FIXTURE}" != "1" && -d "${work_dir}/.terraform" ]]; then
    live_terraform_destroy "${cli_config}" "${TERRAFORM_BIN}" "${work_dir}" || destroy_status=$?
  fi
  if [[ "${KEEP_LIVE_FIXTURE}" != "1" ]]; then
    live_drop_share "${share_alpha}"
    live_drop_share "${share_beta}"
    live_drop_database "${database_alpha}"
    live_drop_database "${database_beta}"
  fi
  return "${destroy_status}"
}
trap cleanup EXIT

echo "==> Live SQL-only blueprint smoke (${RUN_ID})"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" init -backend=false -input=false
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" validate
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false

tenant_databases_json="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -json tenant_databases)"
tenant_shares_json="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" output -json tenant_shares)"

TENANT_DATABASES_JSON="${tenant_databases_json}" \
TENANT_SHARES_JSON="${tenant_shares_json}" \
EXPECTED_ALPHA_DATABASE="${database_alpha}" \
EXPECTED_BETA_DATABASE="${database_beta}" \
EXPECTED_ALPHA_SHARE="${share_alpha}" \
EXPECTED_BETA_SHARE="${share_beta}" \
python3 - <<'PY'
import json
import os
import sys

databases = json.loads(os.environ["TENANT_DATABASES_JSON"])
shares = json.loads(os.environ["TENANT_SHARES_JSON"])

expected_databases = {
    "alpha": os.environ["EXPECTED_ALPHA_DATABASE"],
    "beta": os.environ["EXPECTED_BETA_DATABASE"],
}
expected_shares = {
    "alpha": os.environ["EXPECTED_ALPHA_SHARE"],
    "beta": os.environ["EXPECTED_BETA_SHARE"],
}

if databases != expected_databases:
    print(f"unexpected tenant databases: {databases!r}", file=sys.stderr)
    sys.exit(1)
if shares != expected_shares:
    print(f"unexpected tenant shares: {shares!r}", file=sys.stderr)
    sys.exit(1)
PY

set +e
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" plan -detailed-exitcode -input=false
plan_exit=$?
set -e

if [[ "${plan_exit}" -ne 0 ]]; then
  if [[ "${plan_exit}" -eq 2 ]]; then
    echo "Expected no-op plan after SQL-only blueprint apply, but Terraform reported changes" >&2
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
