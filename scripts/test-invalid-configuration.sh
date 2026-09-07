#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/terraform-test.sh
source "${ROOT_DIR}/scripts/lib/terraform-test.sh"
isolate_offline_test_environment
TERRAFORM_BIN="${TERRAFORM_BIN:-terraform}"
prepare_provider_mirror

# Assert provider diagnostics, not arbitrary words in Terraform's rendered
# output or source snippets. Keep every expected diagnostic beside its fixture.
validate_fixture() {
  local fixture="$1"
  shift
  local work_dir="${ROOT_DIR}/test-results/${fixture}-${RUN_ID}"
  if [[ -e "${work_dir}" ]]; then
    echo "Refusing to reuse existing test directory: ${work_dir}" >&2
    exit 1
  fi
  mkdir -p "${work_dir}"
  cp "${ROOT_DIR}/test-fixtures/${fixture}/main.tf" "${work_dir}/main.tf"
  perl -0pi -e "s/version = \"= 0\\.1\\.0\"/version = \"= ${PROVIDER_VERSION}\"/" "${work_dir}/main.tf"
  local cli_config="${work_dir}/terraformrc"
  write_provider_cli_config "${cli_config}"
  TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" init -backend=false -input=false >/dev/null
  local status=0 diagnostics expected
  TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" validate -json > "${work_dir}/validate.json" || status=$?
  if [[ "$#" == 0 ]]; then
    [[ "${status}" == 0 ]]
    jq -e '.valid == true and .error_count == 0' "${work_dir}/validate.json" >/dev/null
    return
  fi
  if [[ "${status}" != 1 ]] || ! jq -e '.valid == false and .error_count > 0' "${work_dir}/validate.json" >/dev/null; then
    echo "Expected validation errors for ${fixture}, got exit ${status}" >&2
    cat "${work_dir}/validate.json" >&2
    exit 1
  fi
  diagnostics="$(jq -r '[.diagnostics[] | select(.severity == "error") | .summary, .detail] | join("\n")' "${work_dir}/validate.json")"
  for expected in "$@"; do
    if [[ "${diagnostics}" != *"${expected}"* ]]; then
      printf 'Missing diagnostic in %s: %s\n%s\n' "${fixture}" "${expected}" "${diagnostics}" >&2
      exit 1
    fi
  done
}

validate_fixture invalid-configuration \
  'Invalid MotherDuck SQL identifier' \
  'leading or trailing whitespace' \
  'Invalid MotherDuck database type' \
  'Invalid MotherDuck snapshot retention days' \
  'lowercase canonical' \
  'lowercase bare SQL option word' \
  'Invalid MotherDuck data source limit' \
  'Invalid MotherDuck data source offset' \
  'Invalid MotherDuck data source run number' \
  'Invalid MotherDuck REST username' \
  'Invalid MotherDuck UUID' \
  'Invalid MotherDuck Dive embed session hint' \
  'must not include leading or trailing whitespace' \
  'Invalid MotherDuck Flight run wait status' \
  'Invalid MotherDuck Flight run poll interval' \
  'Invalid MotherDuck Flight run timeout' \
  'Invalid MotherDuck Flight config' \
  'reserved and cannot be set' \
  'must not contain "=".' \
  data_path \
  encrypted \
  transient \
  ducklake \
  'non-empty DuckLake storage path' \
  'Invalid MotherDuck table columns' \
  'Invalid MotherDuck table column type' \
  'Invalid MotherDuck SQL option' \
  'Invalid MotherDuck secret parameter' \
  'Invalid MotherDuck secret SQL' \
  'Invalid MotherDuck share access' \
  'Invalid MotherDuck share visibility' \
  'Invalid MotherDuck share update mode' \
  'Invalid MotherDuck share grant username' \
  'Invalid MotherDuck service account username' \
  'Invalid MotherDuck access token name' \
  'Invalid MotherDuck access token TTL' \
  'Invalid MotherDuck access token type' \
  'Invalid MotherDuck Duckling instance size' \
  'Invalid MotherDuck Duckling cooldown seconds' \
  'Invalid MotherDuck read-scaling flock size'

validate_fixture invalid-provider-configuration \
  'Invalid MotherDuck API base URL' \
  'must not include username or password credentials' \
  'Invalid MotherDuck attach mode'

validate_fixture invalid-provider-configuration-shape \
  'must not include a query string or fragment' \
  'Invalid MotherDuck attach mode' \
  'must not include leading or trailing whitespace'

validate_fixture deferred-configuration
