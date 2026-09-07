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
RUN_ID="${RUN_ID//-/_}"

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

rest_dir="${result_dir}/rest"
mkdir -p "${rest_dir}"
for example in service_account access_token duckling_config; do
  rest_name="tf_audit_rest_${RUN_ID}_${example}"
  mkdir -p "${rest_dir}/${example}"
  cp "${ROOT_DIR}/examples/resources/motherduck_${example}/resource.tf" "${rest_dir}/${example}/"
  sed -i.bak "s/analytics_app/${rest_name}/g" "${rest_dir}/${example}/resource.tf"
  rm -f "${rest_dir}/${example}/resource.tf.bak"
  cat >"${rest_dir}/${example}/provider.tf" <<HCL
terraform {
  required_providers { motherduck = { source = "motherduckdb/motherduck", version = "= ${PROVIDER_VERSION}" } }
}
provider "motherduck" {}
HCL
done
for data_source in user_tokens active_accounts; do
  mkdir -p "${rest_dir}/${data_source}"
  cp "${ROOT_DIR}/examples/data-sources/motherduck_${data_source}/data-source.tf" "${rest_dir}/${data_source}/"
  if [[ "${data_source}" == "user_tokens" ]]; then
    sed -i.bak "s/analytics_app/tf_audit_rest_${RUN_ID}_service_account/g" "${rest_dir}/${data_source}/data-source.tf"
    rm -f "${rest_dir}/${data_source}/data-source.tf.bak"
  fi
  cat >"${rest_dir}/${data_source}/provider.tf" <<HCL
terraform {
  required_providers { motherduck = { source = "motherduckdb/motherduck", version = "= ${PROVIDER_VERSION}" } }
}
provider "motherduck" {}
HCL
done

blueprint_dir="${result_dir}/blueprints"
mkdir -p "${blueprint_dir}/writer-bootstrap" "${blueprint_dir}/hypertenancy" "${blueprint_dir}/read-hypertenancy"
for example in writer-bootstrap hypertenancy read-hypertenancy; do
  cp "${ROOT_DIR}/examples/blueprints/${example}"/*.tf "${blueprint_dir}/${example}/"
done
cat >"${blueprint_dir}/hypertenancy/terraform.tfvars" <<HCL
database_prefix = "tf_audit_bp_${RUN_ID}"
reader_prefix   = "tf_audit_reader_${RUN_ID}"
share_prefix    = "tf_audit_share_${RUN_ID}"
tenants = { acme = { display_name = "Acme" } }
HCL
cat >"${blueprint_dir}/read-hypertenancy/terraform.tfvars" <<HCL
expected_writer_username = "tf_audit_bp_writer_${RUN_ID}"
database_prefix = "tf_audit_rh_${RUN_ID}"
reader_prefix   = "tf_audit_reader_rh_${RUN_ID}"
share_prefix    = "tf_audit_share_rh_${RUN_ID}"
tenants = { acme = { display_name = "Acme" } }
HCL

for environment in dev prod; do
  for layout in simple layered; do
    work_dir="${result_dir}/${layout}-${environment}"
    mkdir -p "${work_dir}"
    cp "${ROOT_DIR}/examples/warehouses/${layout}"/*.tf "${ROOT_DIR}/examples/warehouses/${layout}"/*.tftpl "${work_dir}/"
    cp "${ROOT_DIR}/examples/warehouses/${layout}/${environment}.tfvars" "${work_dir}/terraform.tfvars"
  done
done

check_bi_access() {
  local token="$1" writer_token="$2" read_relation="$3" write_relation="$4" log_prefix="$5"
  local read_status=1 read_output write_status=0
  for _ in 1 2 3 4 5 6; do
    set +e
    read_output="$(MOTHERDUCK_TOKEN="${token}" go run "${ROOT_DIR}/internal/dev/mdexec" -scalar \
      "SELECT count(*)::VARCHAR FROM ${read_relation}" 2>"${result_dir}/${log_prefix}-read.err")"
    read_status=$?
    set -e
    if [[ "${read_status}" -eq 0 ]]; then
      printf '%s\n' "${read_output}" >"${result_dir}/${log_prefix}-read.txt"
      break
    fi
    sleep 10
  done
  if [[ "${read_status}" -ne 0 ]]; then
    echo "BI read did not become available for ${read_relation}" >&2
    return 1
  fi
  local probe_id="bi_probe_${RUN_ID}"
  local writer_insert="INSERT INTO ${write_relation} (order_id, order_date, amount, status) VALUES ('${probe_id}', DATE '2026-01-01', 1.00, 'completed')"
  if [[ "${write_relation}" == *"_marts"* ]]; then
    writer_insert="INSERT INTO ${write_relation} (order_date, order_count, revenue) VALUES (DATE '2026-01-01', 1, 1.00)"
  elif [[ "${write_relation}" != *"_simple"* ]]; then
    writer_insert="INSERT INTO ${write_relation} (order_id, order_date, amount, status, source_revision) VALUES ('${probe_id}', DATE '2026-01-01', 1.00, 'completed', 1)"
  fi
  local before_count after_writer_count after_bi_count
  before_count="$(MOTHERDUCK_TOKEN="${writer_token}" go run "${ROOT_DIR}/internal/dev/mdexec" -scalar "SELECT count(*)::VARCHAR FROM ${write_relation}")"
  MOTHERDUCK_TOKEN="${writer_token}" go run "${ROOT_DIR}/internal/dev/mdexec" -sql "${writer_insert}" >"${result_dir}/${log_prefix}-writer-write.txt" 2>&1
  after_writer_count="$(MOTHERDUCK_TOKEN="${writer_token}" go run "${ROOT_DIR}/internal/dev/mdexec" -scalar "SELECT count(*)::VARCHAR FROM ${write_relation}")"
  [[ "${after_writer_count}" -eq $((before_count + 1)) ]] || { echo "writer write did not add one row" >&2; return 1; }
  set +e
  MOTHERDUCK_TOKEN="${token}" go run "${ROOT_DIR}/internal/dev/mdexec" -sql \
    "${writer_insert}" \
    >"${result_dir}/${log_prefix}-write.txt" 2>&1
  write_status=$?
  set -e
  if [[ "${write_status}" -eq 0 ]]; then
    echo "BI write unexpectedly succeeded for ${write_relation}" >&2
    return 1
  fi
  after_bi_count="$(MOTHERDUCK_TOKEN="${writer_token}" go run "${ROOT_DIR}/internal/dev/mdexec" -scalar "SELECT count(*)::VARCHAR FROM ${write_relation}")"
  [[ "${after_bi_count}" -eq "${after_writer_count}" ]] || { echo "BI write changed rowcount" >&2; return 1; }
}

check_cross_environment_denied() {
  local token="$1" relation="$2" log_prefix="$3" status
  set +e
  MOTHERDUCK_TOKEN="${token}" go run "${ROOT_DIR}/internal/dev/mdexec" -scalar \
    "SELECT count(*)::VARCHAR FROM ${relation}" >"${result_dir}/${log_prefix}.txt" 2>&1
  status=$?
  set -e
  if [[ "${status}" -eq 0 ]]; then
    echo "cross-environment BI read unexpectedly succeeded for ${relation}" >&2
    return 1
  fi
}

check_account_status() {
  local expected="$1"
  shift
  local username status
  for username in "$@"; do
    status="$(CHECK_USERNAME="${username}" CHECK_EXPECTED="${expected}" python3 - <<'PY'
import os
import urllib.error
import urllib.parse
import urllib.request

url = os.environ.get("MOTHERDUCK_API_BASE_URL", "https://api.motherduck.com").rstrip("/")
url += "/v1/users/" + urllib.parse.quote(os.environ["CHECK_USERNAME"], safe="") + "/instances"
request = urllib.request.Request(url, headers={"Authorization": "Bearer " + os.environ["MOTHERDUCK_ADMIN_TOKEN"]})
try:
    with urllib.request.urlopen(request, timeout=30) as response:
        print(response.status)
except urllib.error.HTTPError as error:
    print(error.code)
PY
    )"
    [[ "${status}" == "${expected}" ]] || { echo "account ${username} returned HTTP ${status}, expected ${expected}" >&2; return 1; }
  done
}

destroy_status=0
cleanup() {
  local environment layout work_dir token example status
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
  fi
  for example in service_account access_token duckling_config; do
    work_dir="${rest_dir}/${example}"
    if [[ -d "${work_dir}/.terraform" ]]; then
      TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" destroy -auto-approve -input=false \
        >"${result_dir}/rest-${example}-destroy.log" 2>&1 || destroy_status=$?
    fi
  done
  if [[ "${destroy_status}" -ne 0 ]]; then
    echo "REST example cleanup failed; preserving bootstrap state" >&2
  fi
  for example in hypertenancy read-hypertenancy; do
    work_dir="${blueprint_dir}/${example}"
    if [[ -d "${work_dir}/.terraform" ]]; then
      MOTHERDUCK_TOKEN="${bp_writer_token:-}" TF_CLI_CONFIG_FILE="${cli_config}" \
        "${TERRAFORM_BIN}" -chdir="${work_dir}" destroy -auto-approve -input=false \
        >"${result_dir}/blueprint-${example}-destroy.log" 2>&1 || destroy_status=$?
    fi
  done
  if [[ "${destroy_status}" -ne 0 ]]; then
    echo "blueprint data cleanup failed; preserving bootstrap state" >&2
  fi
  if [[ "${destroy_status}" -eq 0 && -d "${blueprint_dir}/writer-bootstrap/.terraform" ]]; then
    TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${blueprint_dir}/writer-bootstrap" \
      destroy -auto-approve -input=false -var="writer_username=tf_audit_bp_writer_${RUN_ID}" \
      >"${result_dir}/blueprint-writer-bootstrap-destroy.log" 2>&1 || destroy_status=$?
  fi
  if [[ "${destroy_status}" -eq 0 && -d "${bootstrap_dir}/.terraform" ]]; then
    TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${bootstrap_dir}" \
      destroy -auto-approve -input=false -var="account_prefix=tf_audit_wh_${RUN_ID}" \
      >"${result_dir}/bootstrap-destroy.log" 2>&1 || destroy_status=$?
  fi
  if [[ "${destroy_status}" -eq 0 ]]; then
    check_account_status 404 \
      "tf_audit_wh_${RUN_ID}_dev_writer" "tf_audit_wh_${RUN_ID}_prod_writer" \
      "tf_audit_rest_${RUN_ID}_service_account" "tf_audit_rest_${RUN_ID}_access_token" \
      "tf_audit_rest_${RUN_ID}_duckling_config" "tf_audit_bp_writer_${RUN_ID}" \
      "tf_audit_reader_${RUN_ID}_acme" "tf_audit_reader_rh_${RUN_ID}_acme" || destroy_status=1
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

for example in service_account access_token duckling_config; do
  work_dir="${rest_dir}/${example}"
  TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" init -backend=false -input=false >/dev/null
  TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" apply -auto-approve -input=false >"${result_dir}/rest-${example}-apply.log" 2>&1
  TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" plan -detailed-exitcode -input=false >"${result_dir}/rest-${example}-plan.log" 2>&1
done
for data_source in user_tokens active_accounts; do
  work_dir="${rest_dir}/${data_source}"
  TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" init -backend=false -input=false >/dev/null
  TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" plan -input=false >"${result_dir}/rest-${data_source}-plan.log" 2>&1
done

TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${blueprint_dir}/writer-bootstrap" init -backend=false -input=false >/dev/null
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${blueprint_dir}/writer-bootstrap" apply -auto-approve -input=false \
  -var="writer_username=tf_audit_bp_writer_${RUN_ID}" >"${result_dir}/blueprint-writer-bootstrap-apply.log" 2>&1
bp_writer_token="$("${TERRAFORM_BIN}" -chdir="${blueprint_dir}/writer-bootstrap" output -raw writer_token)"
for example in hypertenancy read-hypertenancy; do
  TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${blueprint_dir}/${example}" init -backend=false -input=false >/dev/null
  MOTHERDUCK_TOKEN="${bp_writer_token}" TF_CLI_CONFIG_FILE="${cli_config}" \
    "${TERRAFORM_BIN}" -chdir="${blueprint_dir}/${example}" apply -auto-approve -input=false \
    >"${result_dir}/blueprint-${example}-apply.log" 2>&1
  MOTHERDUCK_TOKEN="${bp_writer_token}" TF_CLI_CONFIG_FILE="${cli_config}" \
    "${TERRAFORM_BIN}" -chdir="${blueprint_dir}/${example}" plan -detailed-exitcode -input=false \
    >"${result_dir}/blueprint-${example}-plan.log" 2>&1
done

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

check_account_status 200 \
  "tf_audit_wh_${RUN_ID}_dev_writer" "tf_audit_wh_${RUN_ID}_prod_writer" \
  "tf_audit_rest_${RUN_ID}_service_account" "tf_audit_rest_${RUN_ID}_access_token" \
  "tf_audit_rest_${RUN_ID}_duckling_config" "tf_audit_bp_writer_${RUN_ID}" \
  "tf_audit_reader_${RUN_ID}_acme" "tf_audit_reader_rh_${RUN_ID}_acme"

check_bi_access "${dev_bi_token}" "${dev_writer_token}" "tf_audit_wh_${RUN_ID}_dev_simple.analytics.daily_revenue" "tf_audit_wh_${RUN_ID}_dev_simple.raw.orders" dev-bi-simple
check_bi_access "${prod_bi_token}" "${prod_writer_token}" "tf_audit_wh_${RUN_ID}_prod_simple.analytics.daily_revenue" "tf_audit_wh_${RUN_ID}_prod_simple.raw.orders" prod-bi-simple
check_bi_access "${dev_bi_token}" "${dev_writer_token}" "tf_audit_wh_${RUN_ID}_dev_marts.main.daily_revenue" "tf_audit_wh_${RUN_ID}_dev_marts.main.daily_revenue" dev-bi-layered
check_bi_access "${prod_bi_token}" "${prod_writer_token}" "tf_audit_wh_${RUN_ID}_prod_marts.main.daily_revenue" "tf_audit_wh_${RUN_ID}_prod_marts.main.daily_revenue" prod-bi-layered
check_cross_environment_denied "${dev_bi_token}" "tf_audit_wh_${RUN_ID}_prod_simple.analytics.daily_revenue" dev-bi-cross-prod
check_cross_environment_denied "${prod_bi_token}" "tf_audit_wh_${RUN_ID}_dev_simple.analytics.daily_revenue" prod-bi-cross-dev

echo "warehouse example smoke passed: ${result_dir}"
