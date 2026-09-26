#!/usr/bin/env bash
set -euo pipefail
umask 077

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${ROOT_DIR}/scripts/lib/terraform-test.sh"
source "${ROOT_DIR}/scripts/lib/live-common.sh"

isolate_live_test_environment
RUN_ID="${RUN_ID:-$(date +%Y%m%d%H%M%S)_$$}"
PROVIDER_VERSION="${PROVIDER_VERSION:-0.3.0}"
TERRAFORM_BIN="${TERRAFORM_BIN:-terraform}"

require_safe_run_id "${RUN_ID}"
case "${LIVE_EXAMPLE_PHASE:-all}" in all|cfa) ;; *) echo "LIVE_EXAMPLE_PHASE must be all or cfa" >&2; exit 1 ;; esac
: "${MOTHERDUCK_TOKEN:?MOTHERDUCK_TOKEN is required}"
: "${MOTHERDUCK_ADMIN_TOKEN:?MOTHERDUCK_ADMIN_TOKEN is required}"

suffix="$(printf '%s' "${RUN_ID}" | shasum -a 256 | awk '{print substr($1, 1, 12)}')"
result_dir="${ROOT_DIR}/test-results/example-lifecycle-live-${RUN_ID}"
if [[ -e "${result_dir}" ]]; then
  echo "Refusing to reuse existing live fixture: ${result_dir}" >&2
  exit 1
fi
mkdir -p "${result_dir}"
chmod 700 "${result_dir}"

prepare_provider_mirror
cli_config="${result_dir}/terraformrc"
write_provider_cli_config "${cli_config}"

terraform_init() {
  local dir="$1"
  TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${dir}" init -backend=false -input=false >/dev/null
  TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${dir}" validate >/dev/null
}

terraform_apply() {
  local dir="$1"
  shift
  MOTHERDUCK_TOKEN="${MOTHERDUCK_TOKEN}" MOTHERDUCK_ADMIN_TOKEN="${MOTHERDUCK_ADMIN_TOKEN}" \
    TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${dir}" \
    apply -auto-approve -input=false "$@" >/dev/null
}

terraform_plan_json() {
  local dir="$1"
  local plan_file="$2"
  shift 2
  MOTHERDUCK_TOKEN="${MOTHERDUCK_TOKEN}" MOTHERDUCK_ADMIN_TOKEN="${MOTHERDUCK_ADMIN_TOKEN}" \
    TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${dir}" \
    plan -input=false -no-color -out="${plan_file}" "$@" >/dev/null
  TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${dir}" show -json "${plan_file}"
}

destroy_root() {
  local dir="$1"
  if [[ -d "${dir}/.terraform" ]]; then
    MOTHERDUCK_TOKEN="${MOTHERDUCK_TOKEN}" MOTHERDUCK_ADMIN_TOKEN="${MOTHERDUCK_ADMIN_TOKEN}" \
      TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${dir}" \
      destroy -auto-approve -input=false >/dev/null
  fi
}

write_reader_root() {
  local name="$1"
  local module_path="$2"
  local prefix="$3"
  local module_body="$4"
  local dir="${result_dir}/${name}"
  mkdir -p "${dir}"
  cat >"${dir}/main.tf" <<HCL
terraform {
  required_version = ">= 1.5.0"
  required_providers {
    motherduck = {
      source  = "motherduckdb/motherduck"
      version = "= ${PROVIDER_VERSION}"
    }
  }
}

provider "motherduck" {}

variable "reader_token_generations" {
  type    = set(string)
  default = []
}

variable "retire_legacy_reader_token" {
  type    = bool
  default = false
}

module "tenant" {
  source = "${module_path}"
${module_body}
  reader_token_generations   = var.reader_token_generations
  retire_legacy_reader_token = var.retire_legacy_reader_token
}

output "share_urls" {
  value     = module.tenant.share_urls
  sensitive = true
}

output "reader_setup_tokens" {
  value     = module.tenant.reader_setup_tokens
  sensitive = true
}

output "reader_tokens" {
  value     = module.tenant.reader_tokens
  sensitive = true
}

output "reader_rotation_tokens" {
  value     = module.tenant.reader_rotation_tokens
  sensitive = true
}
HCL
  printf '%s\n' "${dir}"
}

reader_lifecycle() {
  local name="$1"
  local module_path="$2"
  local prefix="$3"
  local module_body="$4"
  local dir
  dir="$(write_reader_root "${name}" "${module_path}" "${prefix}" "${module_body}")"
  terraform_init "${dir}"
  terraform_apply "${dir}"

  local share_url setup_token reader_token rotation_token
  share_url="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${dir}" output -json share_urls | jq -r '.acme')"
  setup_token="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${dir}" output -json reader_setup_tokens | jq -r '.acme')"
  reader_token="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${dir}" output -json reader_tokens | jq -r '.acme')"

  local database_name
  database_name="tf_e01_db_${prefix}_acme"
  local facts_relation
  facts_relation="$(sql_identifier "${database_name}").\"app\".\"facts\""
  local schema_ready=0
  for _ in $(seq 1 12); do
    if MOTHERDUCK_TOKEN="${MOTHERDUCK_TOKEN}" go run "${ROOT_DIR}/internal/dev/mdexec" \
      -sql "CREATE TABLE IF NOT EXISTS ${facts_relation} (value INTEGER)" >/dev/null 2>&1; then
      schema_ready=1
      break
    fi
    sleep 5
  done
  if [[ "${schema_ready}" != "1" ]]; then
    echo "Tenant schema was not visible after bounded propagation wait" >&2
    return 1
  fi
  MOTHERDUCK_TOKEN="${MOTHERDUCK_TOKEN}" go run "${ROOT_DIR}/internal/dev/mdexec" \
    -sql "DELETE FROM ${facts_relation}" >/dev/null
  MOTHERDUCK_TOKEN="${MOTHERDUCK_TOKEN}" go run "${ROOT_DIR}/internal/dev/mdexec" \
    -sql "INSERT INTO ${facts_relation} VALUES (321)" >/dev/null
  MOTHERDUCK_TOKEN="${MOTHERDUCK_TOKEN}" go run "${ROOT_DIR}/internal/dev/mdexec" \
    -sql "UPDATE SHARE \"tf_e01_share_${prefix}_acme\"" >/dev/null

  local attach_query
  attach_query="ATTACH '${share_url}' AS reporting"
  local relation_ready=0
  for _ in $(seq 1 12); do
    if [[ "$(MOTHERDUCK_TOKEN="${setup_token}" go run "${ROOT_DIR}/internal/dev/mdexec" -pre "${attach_query}" -scalar 'SELECT count(*)::VARCHAR FROM reporting.app.facts' 2>/dev/null || true)" == "1" ]]; then
      relation_ready=1
      break
    fi
    if [[ "$(MOTHERDUCK_TOKEN="${setup_token}" go run "${ROOT_DIR}/internal/dev/mdexec" -scalar 'SELECT count(*)::VARCHAR FROM reporting.app.facts' 2>/dev/null || true)" == "1" ]]; then
      relation_ready=1
      break
    fi
    sleep 5
  done
  if [[ "${relation_ready}" != "1" ]]; then
    echo "Reader share relation was not visible after bounded propagation wait" >&2
    return 1
  fi
  local setup_value reader_value
  setup_value="$(MOTHERDUCK_TOKEN="${setup_token}" go run "${ROOT_DIR}/internal/dev/mdexec" -scalar 'SELECT sum(value)::VARCHAR FROM reporting.app.facts')"
  reader_value=""
  for _ in $(seq 1 12); do
    reader_value="$(MOTHERDUCK_TOKEN="${reader_token}" go run "${ROOT_DIR}/internal/dev/mdexec" -scalar 'SELECT sum(value)::VARCHAR FROM reporting.app.facts' 2>/dev/null || true)"
    [[ "${reader_value}" == "321" || "${reader_value}" == "321.0" ]] && break
    sleep 5
  done
  [[ "${setup_value}" == "321" || "${setup_value}" == "321.0" ]]
  [[ "${reader_value}" == "321" || "${reader_value}" == "321.0" ]]

  local rotation_tfvars="${dir}/terraform.tfvars"
  cat >"${rotation_tfvars}" <<HCL
reader_token_generations = ["rotation_${suffix}"]
retire_legacy_reader_token = false
HCL
  terraform_apply "${dir}"
  rotation_token="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${dir}" output -json reader_rotation_tokens | jq -r --arg key "acme/rotation_${suffix}" '.[$key]')"
  local rotation_value
  rotation_value=""
  for _ in $(seq 1 12); do
    rotation_value="$(MOTHERDUCK_TOKEN="${rotation_token}" go run "${ROOT_DIR}/internal/dev/mdexec" -scalar 'SELECT sum(value)::VARCHAR FROM reporting.app.facts' 2>/dev/null || true)"
    [[ "${rotation_value}" == "321" || "${rotation_value}" == "321.0" ]] && break
    sleep 5
  done
  [[ "${rotation_value}" == "321" || "${rotation_value}" == "321.0" ]]

  perl -0pi -e 's/^retire_legacy_reader_token\s*=\s*false$/retire_legacy_reader_token = true/m' "${rotation_tfvars}"
  terraform_apply "${dir}"
  if TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${dir}" output -json reader_tokens | jq -e 'has("acme")' >/dev/null; then
    echo "Legacy reader token remained after retirement in ${name}" >&2
    return 1
  fi
  rotation_value="$(MOTHERDUCK_TOKEN="${rotation_token}" go run "${ROOT_DIR}/internal/dev/mdexec" -scalar 'SELECT sum(value)::VARCHAR FROM reporting.app.facts')"
  [[ "${rotation_value}" == "321" || "${rotation_value}" == "321.0" ]]
  MOTHERDUCK_TOKEN="${MOTHERDUCK_TOKEN}" go run "${ROOT_DIR}/internal/dev/mdexec" \
    -sql "DROP TABLE IF EXISTS ${facts_relation}" >/dev/null
  destroy_root "${dir}"
}

writer_lifecycle() {
  local dir="${result_dir}/writer-bootstrap"
  mkdir -p "${dir}"
  cat >"${dir}/main.tf" <<HCL
terraform {
  required_version = ">= 1.5.0"
  required_providers {
    motherduck = {
      source  = "motherduckdb/motherduck"
      version = "= ${PROVIDER_VERSION}"
    }
  }
}

provider "motherduck" {}

variable "writer_username" {
  type = string
}

variable "writer_token_generations" {
  type    = set(string)
  default = []
}

variable "retire_legacy_writer_token" {
  type    = bool
  default = false
}

module "writer" {
  source = "${ROOT_DIR}/examples/blueprints/writer-bootstrap"
  writer_username          = var.writer_username
  writer_token_generations = var.writer_token_generations
  retire_legacy_writer_token = var.retire_legacy_writer_token
}

output "writer_token" {
  value     = module.writer.writer_token
  sensitive = true
}

output "writer_rotation_tokens" {
  value     = module.writer.writer_rotation_tokens
  sensitive = true
}
HCL
  cat >"${dir}/terraform.tfvars" <<HCL
writer_username = "tf_e02_writer_${suffix}"
writer_token_generations = ["rotation_${suffix}"]
HCL
  terraform_init "${dir}"
  terraform_apply "${dir}"
  local legacy_token rotation_token
  legacy_token="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${dir}" output -raw writer_token)"
  rotation_token="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${dir}" output -json writer_rotation_tokens | jq -r --arg key "rotation_${suffix}" '.[$key]')"
  MOTHERDUCK_TOKEN="${legacy_token}" go run "${ROOT_DIR}/internal/dev/mdexec" -scalar 'SELECT 1' >/dev/null
  MOTHERDUCK_TOKEN="${rotation_token}" go run "${ROOT_DIR}/internal/dev/mdexec" -scalar 'SELECT 1' >/dev/null
  TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${dir}" state mv \
    'module.writer.motherduck_access_token.writer_legacy[0]' 'module.writer.motherduck_access_token.writer' >/dev/null
  migration_plan="${dir}/migration.tfplan"
  migration_json="$(terraform_plan_json "${dir}" "${migration_plan}")"
  jq -e '[.resource_changes[] | select(.address == "module.writer.motherduck_access_token.writer_legacy[0]") | .change.actions] | all(. == ["no-op"])' <<<"${migration_json}" >/dev/null
  terraform_apply "${dir}"
  MOTHERDUCK_TOKEN="${legacy_token}" go run "${ROOT_DIR}/internal/dev/mdexec" -scalar 'SELECT 1' >/dev/null
  printf '%s\n' 'retire_legacy_writer_token = true' >>"${dir}/terraform.tfvars"
  terraform_apply "${dir}"
  if [[ -n "$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${dir}" output -raw writer_token 2>/dev/null || true)" ]]; then
    echo "Legacy writer token remained after retirement" >&2
    return 1
  fi
  local legacy_revoked=0
  for _ in $(seq 1 12); do
    if ! MOTHERDUCK_TOKEN="${legacy_token}" go run "${ROOT_DIR}/internal/dev/mdexec" -scalar 'SELECT 1' >"${dir}/legacy-rejected.log" 2>&1; then
      legacy_revoked=1
      break
    fi
    sleep 5
  done
  if [[ "${legacy_revoked}" != "1" ]]; then
    echo "Legacy writer token still authenticated after bounded revocation wait" >&2
    return 1
  fi
  MOTHERDUCK_TOKEN="${rotation_token}" go run "${ROOT_DIR}/internal/dev/mdexec" -scalar 'SELECT 1' >/dev/null
  destroy_root "${dir}"
}

hyper_body="$(printf '  database_prefix = \"tf_e01_db_%s\"\n  share_prefix = \"tf_e01_share_%s\"\n  reader_prefix = \"tf_e01_reader_%s\"\n  tenants = { acme = { display_name = \"Acme\" } }' "${suffix}" "${suffix}" "${suffix}")"
read_body="$(printf '  database_prefix = \"tf_e01_db_%s_read\"\n  share_prefix = \"tf_e01_share_%s_read\"\n  reader_prefix = \"tf_e01_reader_%s_read\"\n  tenants = { acme = { display_name = \"Acme\" } }' "${suffix}" "${suffix}" "${suffix}")"
cleanup() {
  local status=$?
  trap - EXIT
  # Always clean disposable validation infrastructure, including failures.
    # The two module smokes own a validation table outside Terraform state.
    for ending in "${suffix}" "${suffix}_read"; do
      database="tf_e01_db_${ending}_acme"
      if [[ "$(go run "${ROOT_DIR}/internal/dev/mdexec" -scalar "SELECT count(*)::VARCHAR FROM MD_INFORMATION_SCHEMA.DATABASES WHERE name = '${database}'" 2>/dev/null || true)" == 1 ]]; then
        go run "${ROOT_DIR}/internal/dev/mdexec" -sql "DROP TABLE IF EXISTS \"${database}\".app.facts" >"${result_dir}/cleanup-facts-${ending}.log" 2>&1 || status=1
      fi
    done
    for name in customer-facing-analytics read-hypertenancy hypertenancy writer-bootstrap; do
      destroy_root "${result_dir}/${name}" >"${result_dir}/cleanup-${name}.log" 2>&1 || status=1
    done
  [[ "${status}" == 0 ]] || echo "Validation failed. Private logs/state: ${result_dir}" >&2
  exit "${status}"
}
trap cleanup EXIT

if [[ "${LIVE_EXAMPLE_PHASE:-all}" == all ]]; then
  reader_lifecycle hypertenancy "${ROOT_DIR}/examples/blueprints/hypertenancy" "${suffix}" "${hyper_body}"
  reader_lifecycle read-hypertenancy "${ROOT_DIR}/examples/blueprints/read-hypertenancy" "${suffix}_read" "${read_body}"
  writer_lifecycle
fi

cfa_dir="${result_dir}/customer-facing-analytics"
mkdir -p "${cfa_dir}"
cp "${ROOT_DIR}/examples/customer-facing-analytics"/*.tf "${cfa_dir}/"
cat >"${cfa_dir}/terraform.tfvars" <<HCL
name_prefix = "tf_e07_cfa_${suffix}"
tenants = ["acme", "globex"]
HCL
terraform_init "${cfa_dir}"
terraform_apply "${cfa_dir}"

cfa_prefix="tf_e07_cfa_${suffix}"
for tenant_id in acme globex; do
  database_name="${cfa_prefix}_${tenant_id}"
  share_name="${cfa_prefix}_share_${tenant_id}"
  setup_token="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${cfa_dir}" output -json reader_setup_tokens | jq -r --arg tenant "${tenant_id}" '.[$tenant]')"
  relation="$(sql_identifier "${database_name}").\"app\".\"daily_usage\""
  writer_ready=0
  for _ in $(seq 1 12); do
    if MOTHERDUCK_TOKEN="${MOTHERDUCK_TOKEN}" go run "${ROOT_DIR}/internal/dev/mdexec" \
      -scalar "SELECT count(*)::VARCHAR FROM \"${database_name}\".\"app\".\"daily_usage\"" >/dev/null 2>&1; then
      writer_ready=1
      break
    fi
    sleep 5
  done
  if [[ "${writer_ready}" != "1" ]]; then
    echo "Customer-facing table was not visible after bounded propagation wait" >&2
    exit 1
  fi
  MOTHERDUCK_TOKEN="${MOTHERDUCK_TOKEN}" go run "${ROOT_DIR}/internal/dev/mdexec" -database "${database_name}" \
    -sql "DELETE FROM ${relation}" >/dev/null
  sample_count=42
  [[ "${tenant_id}" == "globex" ]] && sample_count=17
  MOTHERDUCK_TOKEN="${MOTHERDUCK_TOKEN}" go run "${ROOT_DIR}/internal/dev/mdexec" -database "${database_name}" \
    -sql "INSERT INTO ${relation} (usage_date, event_count) VALUES (DATE '2026-01-01', ${sample_count})" >/dev/null
  MOTHERDUCK_TOKEN="${MOTHERDUCK_TOKEN}" go run "${ROOT_DIR}/internal/dev/mdexec" \
    -sql "UPDATE SHARE \"${share_name}\"" >/dev/null
  share_url="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${cfa_dir}" output -json share_urls | jq -r --arg tenant "${tenant_id}" '.[$tenant]')"
  attached=0
  for _ in $(seq 1 12); do
    if [[ "$(MOTHERDUCK_TOKEN="${setup_token}" go run "${ROOT_DIR}/internal/dev/mdexec" -pre "ATTACH '${share_url}' AS reporting" -scalar 'SELECT count(*)::VARCHAR FROM reporting.app.daily_usage' 2>/dev/null || true)" == "1" ]]; then
      attached=1
      break
    fi
    if [[ "$(MOTHERDUCK_TOKEN="${setup_token}" go run "${ROOT_DIR}/internal/dev/mdexec" -scalar 'SELECT count(*)::VARCHAR FROM reporting.app.daily_usage' 2>/dev/null || true)" == "1" ]]; then
      attached=1
      break
    fi
    sleep 5
  done
  if [[ "${attached}" != "1" ]]; then
    echo "Customer-facing share was not attached after bounded propagation wait" >&2
    exit 1
  fi
done

backend_dir="${ROOT_DIR}/examples/customer-facing-analytics/backend"
backend_outputs="${cfa_dir}/backend-outputs.json"
chmod 700 "${cfa_dir}"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${cfa_dir}" output -json >"${backend_outputs}"
chmod 600 "${backend_outputs}"
(cd "${backend_dir}" && npm ci --silent)
MOTHERDUCK_PG_HOST="${MOTHERDUCK_PG_HOST:-pg.us-east-1-aws.motherduck.com}" \
  node "${backend_dir}/live-smoke.mjs" "${backend_outputs}"

suspended_plan="${cfa_dir}/suspended.tfplan"
suspended_json="$(terraform_plan_json "${cfa_dir}" "${suspended_plan}" -var='suspended_tenants=["acme"]')"
jq -e '
  ([.resource_changes[] | select(.address == "motherduck_access_token.reader[\"acme\"]") | .change.actions] == [["delete"]]) and
  ([.resource_changes[] | select(.address == "motherduck_share_grant.reader[\"acme\"]") | .change.actions] == [["delete"]]) and
  ([.resource_changes[] | select((.address | endswith("[\"acme\"]")) and (.type | IN("motherduck_database", "motherduck_table", "motherduck_schema", "motherduck_share", "motherduck_service_account", "motherduck_duckling_config"))) | .change.actions] == [["no-op"],["no-op"],["no-op"],["no-op"],["no-op"],["no-op"]])
' <<<"${suspended_json}" >/dev/null
terraform_apply "${cfa_dir}" -var='suspended_tenants=["acme"]'
jq -e '.acme.suspended == true' < <(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${cfa_dir}" output -json tenants) >/dev/null
MOTHERDUCK_PG_HOST="${MOTHERDUCK_PG_HOST:-pg.us-east-1-aws.motherduck.com}" \
  node "${backend_dir}/revocation-smoke.mjs" "${backend_outputs}"
retained="$(go run "${ROOT_DIR}/internal/dev/mdexec" -scalar "SELECT event_count::VARCHAR FROM \"${cfa_prefix}_acme\".app.daily_usage")"
[[ "${retained}" == 42 ]]
terraform_apply "${cfa_dir}"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${cfa_dir}" output -json >"${backend_outputs}"
MOTHERDUCK_PG_HOST="${MOTHERDUCK_PG_HOST:-pg.us-east-1-aws.motherduck.com}" \
  node "${backend_dir}/live-smoke.mjs" "${backend_outputs}"
TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${cfa_dir}" plan -input=false -detailed-exitcode >/dev/null
destroy_root "${cfa_dir}"

printf 'Example lifecycle live checks passed. Private logs/state: %s\n' "${result_dir}"
