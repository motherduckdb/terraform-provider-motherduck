#!/usr/bin/env bash
set -euo pipefail

# Live smoke for the writer-ownership blueprint path:
# stage one mints a writer service account and token with admin credentials,
# stage two applies tenant data infrastructure with the writer token as
# MOTHERDUCK_TOKEN, proving the writer owns the tenant database (write path)
# and the share (GRANT READ ON SHARE to the reader is permitted).

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/terraform-test.sh
source "${ROOT_DIR}/scripts/lib/terraform-test.sh"

PROVIDER_VERSION="${PROVIDER_VERSION:-0.1.0}"
RUN_ID="${RUN_ID:-$(date +%Y%m%d%H%M%S)_$$}"
source "${ROOT_DIR}/scripts/lib/live-common.sh"
require_safe_run_id "${RUN_ID}"
TERRAFORM_BIN="${TERRAFORM_BIN:-terraform}"
KEEP_LIVE_FIXTURE="${KEEP_LIVE_FIXTURE:-0}"

if [[ -z "${MOTHERDUCK_TOKEN:-}" ]]; then
  echo "MOTHERDUCK_TOKEN is required for SQL smoke tests" >&2
  exit 1
fi
if [[ -z "${MOTHERDUCK_ADMIN_TOKEN:-}" ]]; then
  echo "MOTHERDUCK_ADMIN_TOKEN is required for writer bootstrap REST operations" >&2
  exit 1
fi

prepare_provider_mirror

result_dir="${ROOT_DIR}/test-results/live-writer-path-${RUN_ID}"
if [[ -e "${result_dir}" ]]; then
  echo "Refusing to reuse existing test directory: ${result_dir}" >&2
  exit 1
fi
bootstrap_dir="${result_dir}/bootstrap"
tenant_dir="${result_dir}/tenant-data"
mkdir -p "${bootstrap_dir}" "${tenant_dir}"
cp "${ROOT_DIR}/test-fixtures/live-writer-bootstrap/main.tf" "${bootstrap_dir}/main.tf"
cp "${ROOT_DIR}/test-fixtures/live-writer-tenant-data/main.tf" "${tenant_dir}/main.tf"
perl -0pi -e "s/version = \"= 0\\.1\\.0\"/version = \"= ${PROVIDER_VERSION}\"/" "${bootstrap_dir}/main.tf" "${tenant_dir}/main.tf"

cli_config="${result_dir}/terraformrc"
write_provider_cli_config "${cli_config}"

suffix="${RUN_ID//-/_}"
writer_username="tf_writer_${suffix}"
tenant_database="tf_writerpath_${suffix}"
tenant_share="tf_writerpath_share_${suffix}"
writer_token=""

audit_accounts_destroyed() {
  TEST_ACCOUNT_SUFFIX="${RUN_ID//-/_}" python3 - <<'PYTHON'
import os
import urllib.error
import urllib.parse
import urllib.request

base = os.environ.get("MOTHERDUCK_API_BASE_URL", "https://api.motherduck.com").rstrip("/")
for prefix in ("tf_writer_", "tf_wreader_"):
    name = prefix + os.environ["TEST_ACCOUNT_SUFFIX"]
    request = urllib.request.Request(base + "/v1/users/" + urllib.parse.quote(name, safe="") + "/instances",
        headers={"Authorization": "Bearer " + os.environ["MOTHERDUCK_ADMIN_TOKEN"]})
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            raise SystemExit("Test account remains after destroy: " + name)
    except urllib.error.HTTPError as error:
        if error.code != 404:
            raise SystemExit("Account cleanup check failed with HTTP " + str(error.code))
print("Writer and reader accounts are absent after cleanup")
PYTHON
}

cleanup() {
  local destroy_status=0
  if [[ "${KEEP_LIVE_FIXTURE}" != "1" ]]; then
    if [[ -n "${writer_token}" && -d "${tenant_dir}/.terraform" ]]; then
      MOTHERDUCK_TOKEN="${writer_token}" \
        live_terraform_destroy "${cli_config}" "${TERRAFORM_BIN}" "${tenant_dir}" \
        -var "run_id=${RUN_ID}" -var "expected_writer_username=${writer_username}" || destroy_status=$?
      if [[ -n "${writer_token}" ]]; then
        MOTHERDUCK_TOKEN="${writer_token}" live_drop_share "${tenant_share}" || destroy_status=$?
        MOTHERDUCK_TOKEN="${writer_token}" live_drop_database "${tenant_database}" || destroy_status=$?
      fi
    fi
    # Keep the identity and token available to retry failed tenant cleanup.
    if [[ "${destroy_status}" != 0 ]]; then
      echo "Keeping bootstrap credentials because tenant cleanup failed: ${tenant_dir}" >&2
      return "${destroy_status}"
    fi
    if [[ -d "${bootstrap_dir}/.terraform" ]]; then
      live_terraform_destroy "${cli_config}" "${TERRAFORM_BIN}" "${bootstrap_dir}" \
        -var "run_id=${RUN_ID}" || destroy_status=$?
    fi
    if [[ "${destroy_status}" -eq 0 ]]; then
      audit_accounts_destroyed || destroy_status=$?
    fi
  fi
  return "${destroy_status}"
}
trap live_cleanup_on_exit EXIT

echo "==> Live writer-path blueprint smoke (${RUN_ID})"

echo "==> Stage 1: writer bootstrap (admin credentials)"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${bootstrap_dir}" init -backend=false -input=false
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${bootstrap_dir}" validate
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${bootstrap_dir}" apply -auto-approve -input=false -var "run_id=${RUN_ID}"

writer_token="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${bootstrap_dir}" output -raw writer_token)"
bootstrap_username="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${bootstrap_dir}" output -raw writer_username)"
if [[ -z "${writer_token}" ]]; then
  echo "Expected stage 1 to output a non-empty writer token" >&2
  exit 1
fi
if [[ "${bootstrap_username}" != "${writer_username}" ]]; then
  echo "Expected writer username ${writer_username}, got ${bootstrap_username}" >&2
  exit 1
fi

echo "==> Stage 2: tenant data plane as the writer identity"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${tenant_dir}" init -backend=false -input=false
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${tenant_dir}" validate
MOTHERDUCK_TOKEN="${writer_token}" \
  TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${tenant_dir}" apply -auto-approve -input=false \
  -var "run_id=${RUN_ID}" -var "expected_writer_username=${writer_username}"

current_user="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${tenant_dir}" output -raw current_user)"
if [[ "${current_user}" != "${writer_username}" ]]; then
  echo "Expected stage 2 SQL identity ${writer_username}, got ${current_user}" >&2
  exit 1
fi

set +e
MOTHERDUCK_TOKEN="${writer_token}" \
  TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${tenant_dir}" plan -detailed-exitcode -input=false \
  -var "run_id=${RUN_ID}" -var "expected_writer_username=${writer_username}"
plan_exit=$?
set -e

if [[ "${plan_exit}" -ne 0 ]]; then
  if [[ "${plan_exit}" -eq 2 ]]; then
    echo "Expected no-op plan after writer-path apply, but Terraform reported changes" >&2
  fi
  exit "${plan_exit}"
fi

catalog_matches="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${tenant_dir}" output -raw share_catalog_matches)"
if [[ "${catalog_matches}" != "true" ]]; then
  echo "Filtered share catalog did not match the provisioned database" >&2
  exit 1
fi

# Verify the credentials and actual data path, not just catalog existence.
reader_token="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${tenant_dir}" output -raw reader_token)"
reader_setup_token="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${tenant_dir}" output -raw reader_setup_token)"
share_url="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${tenant_dir}" output -raw share_url)"
MOTHERDUCK_TOKEN="${writer_token}" go run "${ROOT_DIR}/internal/dev/mdexec" -sql "INSERT INTO $(sql_identifier "${tenant_database}").app.facts (tenant_id, amount) VALUES ('test', 321)"
MOTHERDUCK_TOKEN="${writer_token}" go run "${ROOT_DIR}/internal/dev/mdexec" -sql "UPDATE SHARE $(sql_identifier "${tenant_share}")"
attach="ATTACH $(sql_literal "${share_url}") AS $(sql_identifier "${tenant_database}")"
query="SELECT sum(amount)::VARCHAR FROM $(sql_identifier "${tenant_database}").app.facts"
setup_rows="$(MOTHERDUCK_TOKEN="${reader_setup_token}" go run "${ROOT_DIR}/internal/dev/mdexec" -pre "${attach}" -scalar "${query}")"
reader_rows="$(MOTHERDUCK_TOKEN="${reader_token}" go run "${ROOT_DIR}/internal/dev/mdexec" -scalar "${query}")"
if [[ "${setup_rows}" != "321.0" || "${reader_rows}" != "321.0" ]]; then
  echo "Expected writer data to be readable through both reader tokens" >&2
  exit 1
fi
if MOTHERDUCK_TOKEN="${reader_token}" go run "${ROOT_DIR}/internal/dev/mdexec" \
  -sql "INSERT INTO $(sql_identifier "${tenant_database}").app.facts (tenant_id, amount) VALUES ('denied', 999)" > "${result_dir}/reader-write.log" 2>&1; then
  echo "Read-scaling token unexpectedly wrote to the shared database" >&2
  exit 1
fi
writer_rows="$(MOTHERDUCK_TOKEN="${writer_token}" go run "${ROOT_DIR}/internal/dev/mdexec" -scalar "${query}")"
if [[ "${writer_rows}" != "321.0" ]]; then
  echo "Reader denial check changed writer data" >&2
  exit 1
fi
echo "Writer data is readable through the share and reader writes are rejected"

if [[ "${KEEP_LIVE_FIXTURE}" == "1" ]]; then
  trap - EXIT
  echo "Kept live fixture at ${result_dir}"
fi
