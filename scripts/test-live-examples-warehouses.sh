#!/usr/bin/env bash
# shellcheck disable=SC1091
set -euo pipefail

umask 077

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/terraform-test.sh
source "${ROOT_DIR}/scripts/lib/terraform-test.sh"
# shellcheck source=scripts/lib/live-common.sh
source "${ROOT_DIR}/scripts/lib/live-common.sh"

PROVIDER_VERSION="${PROVIDER_VERSION:-0.1.1}"
RUN_ID="${RUN_ID:-$(date +%Y%m%d%H%M%S)_$$}"
TERRAFORM_BIN="${TERRAFORM_BIN:-terraform}"
require_safe_run_id "${RUN_ID}"

if [[ -z "${MOTHERDUCK_ADMIN_TOKEN:-}" ]]; then
  echo "MOTHERDUCK_ADMIN_TOKEN is required for warehouse example smoke tests" >&2
  exit 1
fi
command -v curl >/dev/null 2>&1 || { echo "curl is required for cleanup readback" >&2; exit 1; }

result_dir="${ROOT_DIR}/test-results/live-examples-warehouses-${RUN_ID}"
if [[ -e "${result_dir}" ]]; then
  echo "Refusing to reuse existing test directory: ${result_dir}" >&2
  exit 1
fi
mkdir -p "${result_dir}"

# prepare_provider_mirror reads this dynamically.
# shellcheck disable=SC2034
mirror_dir="${result_dir}/provider-mirror"
prepare_provider_mirror
cli_config="${result_dir}/terraformrc"
write_provider_cli_config "${cli_config}"

bootstrap_dir="${result_dir}/bootstrap"
mkdir -p "${bootstrap_dir}"
cp "${ROOT_DIR}/examples/warehouses/bootstrap"/*.tf "${bootstrap_dir}/"

for environment in dev prod; do
  for layout in simple layered; do
    work_dir="${result_dir}/${layout}-${environment}"
    mkdir -p "${work_dir}"
    cp "${ROOT_DIR}/examples/warehouses/${layout}"/*.tf "${ROOT_DIR}/examples/warehouses/${layout}"/*.tftpl "${work_dir}/"
    cp "${ROOT_DIR}/examples/warehouses/${layout}/${environment}.tfvars" "${work_dir}/terraform.tfvars"
  done
done

check_bi_access() {
  local token="$1" relation="$2" log_prefix="$3"
  local read_status=1 read_output write_status=0
  for _ in 1 2 3 4 5 6; do
    set +e
    read_output="$(MOTHERDUCK_TOKEN="${token}" go run "${ROOT_DIR}/internal/dev/mdexec" -scalar \
      "SELECT count(*)::VARCHAR FROM ${relation}" 2>"${result_dir}/${log_prefix}-read.err")"
    read_status=$?
    set -e
    if [[ "${read_status}" -eq 0 ]]; then
      printf '%s\n' "${read_output}" >"${result_dir}/${log_prefix}-read.txt"
      break
    fi
    sleep 10
  done
  if [[ "${read_status}" -ne 0 ]]; then
    echo "BI read did not become available for ${relation}" >&2
    return 1
  fi
  set +e
  MOTHERDUCK_TOKEN="${token}" go run "${ROOT_DIR}/internal/dev/mdexec" -sql \
    "INSERT INTO ${relation} VALUES (DATE '2026-01-01', 1, 1.00)" \
    >"${result_dir}/${log_prefix}-write.txt" 2>&1
  write_status=$?
  set -e
  if [[ "${write_status}" -eq 0 ]]; then
    echo "BI write unexpectedly succeeded for ${relation}" >&2
    return 1
  fi
}

destroy_status=0
cleanup() {
  local environment layout work_dir token
  local child_status=0
  for environment in prod dev; do
    if [[ "${environment}" == "dev" ]]; then token="${dev_writer_token:-}"; else token="${prod_writer_token:-}"; fi
    [[ -n "${token}" ]] || continue
    for layout in layered simple; do
      work_dir="${result_dir}/${layout}-${environment}"
      if [[ -d "${work_dir}/.terraform" ]]; then
        MOTHERDUCK_TOKEN="${token}" TF_CLI_CONFIG_FILE="${cli_config}" \
          "${TERRAFORM_BIN}" -chdir="${work_dir}" destroy -auto-approve -input=false \
          -var="name_prefix=tf_audit_wh_${RUN_ID}" >"${result_dir}/${layout}-${environment}-destroy.log" 2>&1 || child_status=$?
      fi
    done
  done
  destroy_status="${child_status}"
  if [[ "${destroy_status}" -ne 0 ]]; then
    echo "warehouse cleanup failed; preserving bootstrap state and credentials" >&2
    return "${destroy_status}"
  fi
  if [[ -d "${bootstrap_dir}/.terraform" ]]; then
    TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${bootstrap_dir}" \
      destroy -auto-approve -input=false -var="account_prefix=tf_audit_wh_${RUN_ID}" \
      >"${result_dir}/bootstrap-destroy.log" 2>&1 || destroy_status=$?
  fi
  if [[ "${destroy_status}" -eq 0 ]]; then
    for username in "tf_audit_wh_${RUN_ID}_dev_writer" "tf_audit_wh_${RUN_ID}_prod_writer"; do
      status="$(curl -sS -o /dev/null -w '%{http_code}' \
        -H "Authorization: Bearer ${MOTHERDUCK_ADMIN_TOKEN}" \
        -H 'Accept: application/json' "${MOTHERDUCK_API_BASE_URL:-https://api.motherduck.com}/v1/users/${username}/instances")"
      [[ "${status}" == "404" ]] || { echo "cleanup readback for ${username} returned HTTP ${status}" >&2; destroy_status=1; }
    done
  fi
  return "${destroy_status}"
}
trap live_cleanup_on_exit EXIT

TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${bootstrap_dir}" init -backend=false -input=false >/dev/null
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${bootstrap_dir}" apply -auto-approve -input=false \
  -var="account_prefix=tf_audit_wh_${RUN_ID}" >"${result_dir}/bootstrap-apply.log" 2>&1
tokens_json="$("${TERRAFORM_BIN}" -chdir="${bootstrap_dir}" output -json tokens)"
dev_writer_token="$(jq -r '.dev_writer' <<<"${tokens_json}")"
prod_writer_token="$(jq -r '.prod_writer' <<<"${tokens_json}")"
dev_bi_token="$(jq -r '.dev_bi' <<<"${tokens_json}")"
prod_bi_token="$(jq -r '.prod_bi' <<<"${tokens_json}")"
printf '%s\n' 'captured bootstrap tokens in process memory only' >"${result_dir}/credential-capture.log"

for environment in dev prod; do
  if [[ "${environment}" == "dev" ]]; then token="${dev_writer_token}"; else token="${prod_writer_token}"; fi
  for layout in simple layered; do
    work_dir="${result_dir}/${layout}-${environment}"
    TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" init -backend=false -input=false >/dev/null
    MOTHERDUCK_TOKEN="${token}" TF_CLI_CONFIG_FILE="${cli_config}" \
      "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false \
      -var="name_prefix=tf_audit_wh_${RUN_ID}" >"${result_dir}/${layout}-${environment}-apply.log" 2>&1
    MOTHERDUCK_TOKEN="${token}" TF_CLI_CONFIG_FILE="${cli_config}" \
      "${TERRAFORM_BIN}" -chdir="${work_dir}" plan -detailed-exitcode -input=false \
      -var="name_prefix=tf_audit_wh_${RUN_ID}" >"${result_dir}/${layout}-${environment}-plan.log" 2>&1
  done
done

check_bi_access "${dev_bi_token}" "tf_audit_wh_${RUN_ID}_dev_simple.analytics.daily_revenue" dev-bi-simple
check_bi_access "${prod_bi_token}" "tf_audit_wh_${RUN_ID}_prod_simple.analytics.daily_revenue" prod-bi-simple
check_bi_access "${dev_bi_token}" "tf_audit_wh_${RUN_ID}_dev_marts.main.daily_revenue" dev-bi-layered
check_bi_access "${prod_bi_token}" "tf_audit_wh_${RUN_ID}_prod_marts.main.daily_revenue" prod-bi-layered

echo "warehouse example smoke passed: ${result_dir}"
