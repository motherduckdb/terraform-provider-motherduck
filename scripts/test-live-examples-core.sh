#!/usr/bin/env bash
set -euo pipefail

# This script is deliberately self-contained: every Terraform root is copied
# from the public example it exercises, and every remote object name carries
# the run suffix. Keep credentials in environment variables and Terraform's
# private state only. The first operation must keep generated state private.
umask 077

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/terraform-test.sh
source "${ROOT_DIR}/scripts/lib/terraform-test.sh"
# shellcheck source=scripts/lib/live-common.sh
source "${ROOT_DIR}/scripts/lib/live-common.sh"

isolate_live_test_environment

RUN_ID="${RUN_ID:-$(date +%Y%m%d%H%M%S)_$$}"
PROVIDER_VERSION="${PROVIDER_VERSION:-0.1.0}"
TERRAFORM_BIN="${TERRAFORM_BIN:-terraform}"
KEEP_LIVE_FIXTURE="${KEEP_LIVE_FIXTURE:-0}"

require_safe_run_id "${RUN_ID}"
if ! terraform_path="$(command -v "${TERRAFORM_BIN}")" || [[ ! -x "${terraform_path}" ]]; then
  echo "TERRAFORM_BIN must name an executable Terraform or OpenTofu binary" >&2
  exit 1
fi
if [[ -z "${MOTHERDUCK_ADMIN_TOKEN:-}" ]]; then
  echo "MOTHERDUCK_ADMIN_TOKEN is required for the live core example suite" >&2
  exit 1
fi
if [[ -z "${MOTHERDUCK_TOKEN:-}" ]]; then
  echo "MOTHERDUCK_TOKEN is required for the live core example suite" >&2
  exit 1
fi

# Hyphens are valid in a shell run id but not in the service-account names and
# SQL identifiers used by this suite. Keep the original run id in paths and
# hash the normalized physical suffix so service-account names stay short.
normalized_suffix="${RUN_ID//-/_}"
if [[ ! "${normalized_suffix}" =~ ^[A-Za-z0-9_]+$ ]]; then
  echo "RUN_ID contains characters that are unsafe after hyphen normalization" >&2
  exit 1
fi
if command -v shasum >/dev/null 2>&1; then
  physical_suffix="$(printf '%s' "${normalized_suffix}" | shasum -a 256 | awk '{print substr($1, 1, 16)}')"
elif command -v sha256sum >/dev/null 2>&1; then
  physical_suffix="$(printf '%s' "${normalized_suffix}" | sha256sum | awk '{print substr($1, 1, 16)}')"
else
  echo "shasum or sha256sum is required for generated MotherDuck names" >&2
  exit 1
fi
suffix="${physical_suffix}"

root_test_dir="${ROOT_DIR}/test-results/live-examples-core-${RUN_ID}"
if [[ -e "${root_test_dir}" ]]; then
  echo "Refusing to reuse existing test directory for run id: ${RUN_ID}" >&2
  exit 1
fi
mkdir -p "${root_test_dir}"
chmod 700 "${root_test_dir}"

prepare_provider_mirror
cli_config="${root_test_dir}/terraformrc"
write_provider_cli_config "${cli_config}"

# The actual import.sh examples invoke the command by its conventional name.
# A private shim preserves that format while honoring TERRAFORM_BIN (including
# OpenTofu) for the whole suite.
terraform_shim="${root_test_dir}/bin"
mkdir -p "${terraform_shim}"
ln -s "${terraform_path}" "${terraform_shim}/terraform"

coverage_file="${root_test_dir}/coverage-matrix.tsv"
printf 'stage\tpath\tstatus\n' > "${coverage_file}"
record_coverage() {
  local stage="$1"
  local path="$2"
  local status="$3"
  printf '%s\t%s\t%s\n' "${stage}" "${path}" "${status}" >> "${coverage_file}"
}

# Names are intentionally distinct for every resource example. The share
# fixture is retained until all reader data-source checks have completed.
database_name="tf_core_database_${suffix}"
schema_database_name="tf_core_schema_${suffix}"
schema_name="tf_core_schema_app_${suffix}"
table_database_name="tf_core_table_${suffix}"
table_schema_name="tf_core_table_app_${suffix}"
table_name="tf_core_table_events_${suffix}"
view_database_name="tf_core_view_${suffix}"
view_schema_name="tf_core_view_app_${suffix}"
view_table_name="tf_core_view_events_${suffix}"
view_name="tf_core_view_recent_${suffix}"
snapshot_database_name="tf_core_snapshot_${suffix}"
snapshot_name="tf_core_snapshot_named_${suffix}"
snapshot_updated_name="tf_core_snapshot_renamed_${suffix}"
snapshot_current_name="${snapshot_name}"
share_database_name="tf_core_share_${suffix}"
share_table_name="tf_core_share_orders_${suffix}"
share_view_name="tf_core_share_revenue_${suffix}"
share_name="tf_core_share_${suffix}"
share_grant_database_name="tf_core_grant_${suffix}"
share_grant_table_name="tf_core_grant_orders_${suffix}"
share_grant_name="tf_core_grant_share_${suffix}"
share_grant_role_name="tf_core_readers_${suffix}"
discoverable_share_name="tf_core_discoverable_share_${suffix}"
secret_name="tf_core_secret_${suffix}"

writer_username="tf_core_writer_${suffix}"
reader_username="tf_core_reader_${suffix}"
writer_token=""
reader_workspace_token=""
reader_token=""

bootstrap_writer_dir="${root_test_dir}/bootstrap-writer"
bootstrap_reader_dir="${root_test_dir}/bootstrap-reader"

declare -a child_dirs=()
declare -a child_tokens=()
register_child() {
  child_dirs+=("$1")
  child_tokens+=("$2")
}

storage_path="${MD_TF_ACC_OBJECT_STORAGE_PATH:-}"
if [[ -z "${storage_path}" && -n "${MD_TF_AUDIT_BUCKET:-}" ]]; then
  storage_path="s3://${MD_TF_AUDIT_BUCKET}/${MD_TF_AUDIT_PREFIX:-}"
fi
storage_bucket="${MD_TF_AUDIT_BUCKET:-}"
if [[ -z "${storage_bucket}" && "${storage_path}" == s3://* ]]; then
  storage_bucket="${storage_path#s3://}"
  storage_bucket="${storage_bucket%%/*}"
fi
storage_fixture_path=""
storage_fixture_uri=""
storage_probe_uri=""
storage_probe_uploaded=0
discoverable_share_created=0
shared_catalog_name="${share_name}"
shared_with_me_expected=""
storage_unavailable=0
storage_unavailable_reason=""
storage_hard_failure=0
storage_secret_real=0
if [[ -n "${storage_path}" ]]; then
  storage_fixture_path="${storage_path%/}/fixtures/"
  storage_fixture_uri="${storage_fixture_path%/}/orders.csv"
fi
if [[ -n "${storage_path}" && -n "${AWS_ACCESS_KEY_ID:-}" && -n "${AWS_SECRET_ACCESS_KEY:-}" && -n "${storage_bucket:-}" && "${storage_path}" == s3://* ]]; then
  storage_secret_real=1
  storage_probe_uri="${storage_path%/}/core_${suffix}/probe.csv"
elif [[ -z "${storage_path}" ]]; then
  storage_unavailable=1
  storage_unavailable_reason="MD_TF_ACC_OBJECT_STORAGE_PATH (or MD_TF_AUDIT_BUCKET) is unset"
elif [[ -z "${AWS_ACCESS_KEY_ID:-}" || -z "${AWS_SECRET_ACCESS_KEY:-}" ]]; then
  storage_unavailable=1
  storage_unavailable_reason="short-lived AWS credentials are unavailable"
fi

replace_text() {
  local file="$1"
  local from="$2"
  local to="$3"
  FROM="${from}" TO="${to}" perl -0pi -e 's/\Q$ENV{FROM}\E/$ENV{TO}/g' "${file}"
}

for_example_file() {
  local dir="$1"
  local file
  for file in "${dir}"/*.tf "${dir}"/import.sh; do
    [[ -f "${file}" ]] || continue
    printf '%s\n' "${file}"
  done
}

replace_example_literal() {
  local dir="$1"
  local canonical="$2"
  local actual="$3"
  local file
  while IFS= read -r file; do
    replace_text "${file}" "\"${canonical}\"" "\"${actual}\""
    replace_text "${file}" "'${canonical}'" "'${actual}'"
  done < <(for_example_file "${dir}")
}

restore_resource_label() {
  local dir="$1"
  local resource_type="$2"
  local canonical="$3"
  local actual="$4"
  local file
  while IFS= read -r file; do
    replace_text "${file}" "\"${resource_type}\" \"${actual}\"" "\"${resource_type}\" \"${canonical}\""
  done < <(for_example_file "${dir}")
}

copy_actual_example() {
  local kind="$1"
  local target="$2"
  local source="${ROOT_DIR}/examples/resources/motherduck_${kind}"
  local file
  local copied=0
  mkdir -p "${target}"
  for file in "${source}"/*.tf; do
    [[ -f "${file}" ]] || continue
    cp "${file}" "${target}/"
    copied=1
  done
  if [[ "${copied}" -eq 0 ]]; then
    echo "No Terraform files found for resource example ${kind}" >&2
    return 1
  fi
  if [[ -f "${source}/import.sh" ]]; then
    cp "${source}/import.sh" "${target}/import.sh"
  fi
  cp "${ROOT_DIR}/examples/blueprints/writer-bootstrap/versions.tf" "${target}/versions.tf"
}

copy_actual_data_source() {
  local kind="$1"
  local target="$2"
  local source="${ROOT_DIR}/examples/data-sources/motherduck_${kind}"
  local file
  local copied=0
  mkdir -p "${target}"
  for file in "${source}"/*.tf; do
    [[ -f "${file}" ]] || continue
    cp "${file}" "${target}/"
    copied=1
  done
  if [[ "${copied}" -eq 0 ]]; then
    echo "No Terraform files found for data-source example ${kind}" >&2
    return 1
  fi
  cp "${ROOT_DIR}/examples/blueprints/writer-bootstrap/versions.tf" "${target}/versions.tf"
}

write_provider() {
  local dir="$1"
  local database="${2:-}"
  if [[ -n "${database}" ]]; then
    cat > "${dir}/provider.tf" <<HCL
provider "motherduck" {
  database = "${database}"
}
HCL
  else
    cat > "${dir}/provider.tf" <<HCL
provider "motherduck" {}
HCL
  fi
}

substitute_resource_example() {
  local kind="$1"
  local dir="$2"
  case "${kind}" in
    database)
      replace_example_literal "${dir}" analytics "${database_name}"
      restore_resource_label "${dir}" motherduck_database analytics "${database_name}"
      ;;
    schema)
      replace_example_literal "${dir}" analytics "${schema_database_name}"
      replace_example_literal "${dir}" mart "${schema_name}"
      restore_resource_label "${dir}" motherduck_database analytics "${schema_database_name}"
      restore_resource_label "${dir}" motherduck_schema mart "${schema_name}"
      replace_text "${dir}/import.sh" "analytics.mart" "${schema_database_name}.${schema_name}"
      ;;
    table)
      replace_example_literal "${dir}" analytics "${table_database_name}"
      replace_example_literal "${dir}" mart "${table_schema_name}"
      replace_example_literal "${dir}" events "${table_name}"
      restore_resource_label "${dir}" motherduck_database analytics "${table_database_name}"
      restore_resource_label "${dir}" motherduck_schema mart "${table_schema_name}"
      restore_resource_label "${dir}" motherduck_table events "${table_name}"
      replace_text "${dir}/import.sh" "analytics.mart.events" "${table_database_name}.${table_schema_name}.${table_name}"
      ;;
    view)
      replace_example_literal "${dir}" analytics "${view_database_name}"
      replace_example_literal "${dir}" mart "${view_schema_name}"
      replace_example_literal "${dir}" events "${view_table_name}"
      replace_example_literal "${dir}" recent_events "${view_name}"
      restore_resource_label "${dir}" motherduck_database analytics "${view_database_name}"
      restore_resource_label "${dir}" motherduck_schema mart "${view_schema_name}"
      restore_resource_label "${dir}" motherduck_table events "${view_table_name}"
      restore_resource_label "${dir}" motherduck_view recent_events "${view_name}"
      replace_text "${dir}/import.sh" "analytics.mart.recent_events" "${view_database_name}.${view_schema_name}.${view_name}"
      ;;
    snapshot)
      replace_example_literal "${dir}" analytics "${snapshot_database_name}"
      replace_example_literal "${dir}" analytics_monthly "${snapshot_name}"
      restore_resource_label "${dir}" motherduck_database analytics "${snapshot_database_name}"
      restore_resource_label "${dir}" motherduck_snapshot monthly "${snapshot_name}"
      replace_text "${dir}/import.sh" "analytics.analytics_monthly" "${snapshot_database_name}.${snapshot_name}"
      ;;
    share)
      replace_example_literal "${dir}" analytics "${share_database_name}"
      replace_example_literal "${dir}" orders "${share_table_name}"
      replace_example_literal "${dir}" daily_revenue "${share_view_name}"
      replace_example_literal "${dir}" analytics_share "${share_name}"
      restore_resource_label "${dir}" motherduck_database analytics "${share_database_name}"
      restore_resource_label "${dir}" motherduck_table orders "${share_table_name}"
      restore_resource_label "${dir}" motherduck_view daily_revenue "${share_view_name}"
      restore_resource_label "${dir}" motherduck_share analytics "${share_name}"
      replace_example_literal "${dir}" "main.orders" "main.${share_table_name}"
      replace_example_literal "${dir}" "main.daily_revenue" "main.${share_view_name}"
      replace_text "${dir}/import.sh" "analytics_share" "${share_name}"
      ;;
    share_grant)
      replace_example_literal "${dir}" analytics "${share_grant_database_name}"
      replace_example_literal "${dir}" orders "${share_grant_table_name}"
      replace_example_literal "${dir}" analytics_share "${share_grant_name}"
      replace_example_literal "${dir}" analytics_readers "${share_grant_role_name}"
      restore_resource_label "${dir}" motherduck_database analytics "${share_grant_database_name}"
      restore_resource_label "${dir}" motherduck_table orders "${share_grant_table_name}"
      restore_resource_label "${dir}" motherduck_share analytics "${share_grant_database_name}"
      restore_resource_label "${dir}" motherduck_role analytics_readers "${share_grant_role_name}"
      replace_example_literal "${dir}" "main.orders" "main.${share_grant_table_name}"
      replace_text "${dir}/import.sh" "analytics_share/role/analytics_readers" "${share_grant_name}/role/${share_grant_role_name}"
      ;;
    secret)
      replace_example_literal "${dir}" analytics_s3 "${secret_name}"
      restore_resource_label "${dir}" motherduck_secret s3 "${secret_name}"
      if [[ "${storage_secret_real}" -eq 1 ]]; then
        replace_example_literal "${dir}" "s3://analytics-bucket/" "${storage_path%/}/"
      else
        replace_example_literal "${dir}" "s3://analytics-bucket/" "s3://tf-core-audit-${suffix}/"
      fi
      replace_text "${dir}/import.sh" storage_credentials "${secret_name}"
      ;;
    *)
      echo "Unknown resource example ${kind}" >&2
      return 1
      ;;
  esac
}

substitute_data_source_example() {
  local kind="$1"
  local dir="$2"
  case "${kind}" in
    databases)
      replace_example_literal "${dir}" analytics "${share_database_name}"
      restore_resource_label "${dir}" motherduck_databases analytics "${share_database_name}"
      ;;
    database_snapshots)
      replace_example_literal "${dir}" analytics "${snapshot_database_name}"
      restore_resource_label "${dir}" motherduck_database_snapshots analytics "${snapshot_database_name}"
      ;;
    owned_shares|shared_with_me)
      replace_example_literal "${dir}" analytics_share "${shared_catalog_name}"
      ;;
    owned_share)
      replace_example_literal "${dir}" wikipedia_pageviews "${share_name}"
      ;;
    secrets|buckets_for_secret)
      replace_example_literal "${dir}" analytics_s3 "${secret_name}"
      ;;
    files)
      if [[ -n "${storage_fixture_path}" ]]; then
        replace_example_literal "${dir}" "s3://analytics-bucket/landing/" "${storage_fixture_path}"
      fi
      ;;
    attached_databases|current_user|version|live_duckling_size)
      ;;
    *)
      echo "Unknown data-source example ${kind}" >&2
      return 1
      ;;
  esac
}

run_tf() {
  local token="$1"
  local dir="$2"
  shift 2
  MOTHERDUCK_TOKEN="${token}" TF_CLI_CONFIG_FILE="${cli_config}" env -u MOTHERDUCK_ADMIN_TOKEN "${TERRAFORM_BIN}" -chdir="${dir}" "$@"
}

run_tf_admin_sql() {
  local dir="$1"
  local admin_token="${MOTHERDUCK_ADMIN_TOKEN}"
  shift
  MOTHERDUCK_TOKEN="${admin_token}" MOTHERDUCK_ADMIN_TOKEN="${admin_token}" TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${dir}" "$@"
}

run_tf_secret() {
  local token="$1"
  local dir="$2"
  shift 2
  if [[ -n "${secret_session_value}" ]]; then
    MOTHERDUCK_TOKEN="${token}" TF_CLI_CONFIG_FILE="${cli_config}" TF_VAR_aws_access_key_id="${secret_key_value}" TF_VAR_aws_secret_access_key="${secret_value}" TF_VAR_aws_session_token="${secret_session_value}" env -u MOTHERDUCK_ADMIN_TOKEN "${TERRAFORM_BIN}" -chdir="${dir}" "$@"
  else
    MOTHERDUCK_TOKEN="${token}" TF_CLI_CONFIG_FILE="${cli_config}" TF_VAR_aws_access_key_id="${secret_key_value}" TF_VAR_aws_secret_access_key="${secret_value}" env -u MOTHERDUCK_ADMIN_TOKEN "${TERRAFORM_BIN}" -chdir="${dir}" "$@"
  fi
}

run_tf_provider_example() {
  local dir="$1"
  shift
  MOTHERDUCK_TOKEN="${writer_token}" TF_VAR_motherduck_token="${writer_token}" TF_VAR_motherduck_admin_token="${MOTHERDUCK_ADMIN_TOKEN}" TF_CLI_CONFIG_FILE="${cli_config}" env -u MOTHERDUCK_ADMIN_TOKEN "${TERRAFORM_BIN}" -chdir="${dir}" "$@"
}

run_resource_tf() {
  local kind="$1"
  local dir="$2"
  shift 2
  local token="${writer_token}"
  if [[ "${kind}" == "share_grant" ]]; then
    token="${MOTHERDUCK_ADMIN_TOKEN}"
  fi
  if [[ "${kind}" == "secret" ]]; then
    run_tf_secret "${token}" "${dir}" "$@"
  elif [[ "${kind}" == "share" ]]; then
    MOTHERDUCK_TOKEN="${token}" TF_VAR_reader_username="${reader_username}" TF_CLI_CONFIG_FILE="${cli_config}" env -u MOTHERDUCK_ADMIN_TOKEN "${TERRAFORM_BIN}" -chdir="${dir}" "$@"
  else
    run_tf "${token}" "${dir}" "$@"
  fi
}

run_import_script() {
  local kind="$1"
  local dir="$2"
  local token="${writer_token}"
  if [[ "${kind}" == "share_grant" ]]; then
    token="${MOTHERDUCK_ADMIN_TOKEN}"
  fi
  (
    cd "${dir}"
    if [[ "${kind}" == "share" ]]; then
      MOTHERDUCK_TOKEN="${token}" TF_VAR_reader_username="${reader_username}" TF_CLI_CONFIG_FILE="${cli_config}" PATH="${terraform_shim}:${PATH}" env -u MOTHERDUCK_ADMIN_TOKEN bash ./import.sh
    elif [[ "${kind}" == "secret" ]]; then
      if [[ -n "${secret_session_value}" ]]; then
        MOTHERDUCK_TOKEN="${token}" TF_VAR_aws_access_key_id="${secret_key_value}" TF_VAR_aws_secret_access_key="${secret_value}" TF_VAR_aws_session_token="${secret_session_value}" TF_CLI_CONFIG_FILE="${cli_config}" PATH="${terraform_shim}:${PATH}" env -u MOTHERDUCK_ADMIN_TOKEN bash ./import.sh
      else
        MOTHERDUCK_TOKEN="${token}" TF_VAR_aws_access_key_id="${secret_key_value}" TF_VAR_aws_secret_access_key="${secret_value}" TF_CLI_CONFIG_FILE="${cli_config}" PATH="${terraform_shim}:${PATH}" env -u MOTHERDUCK_ADMIN_TOKEN bash ./import.sh
      fi
    else
      MOTHERDUCK_TOKEN="${token}" TF_CLI_CONFIG_FILE="${cli_config}" PATH="${terraform_shim}:${PATH}" env -u MOTHERDUCK_ADMIN_TOKEN bash ./import.sh
    fi
  )
}

md_scalar() {
  local token="$1"
  local database="$2"
  local query="$3"
  local pre_query="${4:-}"
  if [[ -n "${pre_query}" && -n "${database}" ]]; then
    MOTHERDUCK_TOKEN="${token}" go run "${ROOT_DIR}/internal/dev/mdexec" -database "${database}" -pre "${pre_query}" -scalar "${query}"
  elif [[ -n "${database}" ]]; then
    MOTHERDUCK_TOKEN="${token}" go run "${ROOT_DIR}/internal/dev/mdexec" -database "${database}" -scalar "${query}"
  elif [[ -n "${pre_query}" ]]; then
    MOTHERDUCK_TOKEN="${token}" go run "${ROOT_DIR}/internal/dev/mdexec" -pre "${pre_query}" -scalar "${query}"
  else
    MOTHERDUCK_TOKEN="${token}" go run "${ROOT_DIR}/internal/dev/mdexec" -scalar "${query}"
  fi
}

md_exec() {
  local token="$1"
  local database="$2"
  local query="$3"
  local pre_query="${4:-}"
  if [[ -n "${pre_query}" && -n "${database}" ]]; then
    MOTHERDUCK_TOKEN="${token}" go run "${ROOT_DIR}/internal/dev/mdexec" -database "${database}" -pre "${pre_query}" -sql "${query}"
  elif [[ -n "${database}" ]]; then
    MOTHERDUCK_TOKEN="${token}" go run "${ROOT_DIR}/internal/dev/mdexec" -database "${database}" -sql "${query}"
  elif [[ -n "${pre_query}" ]]; then
    MOTHERDUCK_TOKEN="${token}" go run "${ROOT_DIR}/internal/dev/mdexec" -pre "${pre_query}" -sql "${query}"
  else
    MOTHERDUCK_TOKEN="${token}" go run "${ROOT_DIR}/internal/dev/mdexec" -sql "${query}"
  fi
}

expect_scalar() {
  local label="$1"
  local got="$2"
  local want="$3"
  if [[ "${got}" != "${want}" ]]; then
    echo "Expected ${label}=${want}, got ${got}" >&2
    return 1
  fi
}

expect_nonempty_scalar() {
  local label="$1"
  local got="$2"
  if [[ -z "${got}" ]]; then
    echo "Expected non-empty ${label}" >&2
    return 1
  fi
}

assert_rows_contain() {
  local label="$1"
  local rows_json="$2"
  local expected="$3"
  local mode="${4:-exact}"
  ROWS_JSON="${rows_json}" EXPECTED_VALUE="${expected}" MATCH_MODE="${mode}" python3 - "${label}" <<'PY'
import json
import os
import sys

label = sys.argv[1]
try:
    rows = json.loads(os.environ["ROWS_JSON"])
except (json.JSONDecodeError, TypeError) as exc:
    print(f"{label} returned invalid rows_json: {exc}", file=sys.stderr)
    raise SystemExit(1)

expected = os.environ["EXPECTED_VALUE"].lower()
mode = os.environ["MATCH_MODE"]
if not isinstance(rows, list):
    print(f"{label} returned {type(rows).__name__}, expected a row list", file=sys.stderr)
    raise SystemExit(1)

for row in rows:
    if not isinstance(row, dict):
        continue
    for value in row.values():
        if not isinstance(value, str):
            continue
        candidate = value.lower()
        if (mode == "contains" and expected in candidate) or (mode == "exact" and candidate == expected):
            raise SystemExit(0)

print(f"{label} did not contain fixture value {expected}", file=sys.stderr)
raise SystemExit(1)
PY
}

assert_rows_nonempty() {
  local label="$1"
  local rows_json="$2"
  ROWS_JSON="${rows_json}" python3 - "${label}" <<'PY'
import json
import os
import sys

label = sys.argv[1]
try:
    rows = json.loads(os.environ["ROWS_JSON"])
except (json.JSONDecodeError, TypeError) as exc:
    print(f"{label} returned invalid rows_json: {exc}", file=sys.stderr)
    raise SystemExit(1)
if not isinstance(rows, list) or not rows:
    print(f"{label} returned no rows for the live fixture", file=sys.stderr)
    raise SystemExit(1)
PY
}

assert_rows_json() {
  local label="$1"
  local rows_json="$2"
  ROWS_JSON="${rows_json}" python3 - "${label}" <<'PY'
import json
import os
import sys

label = sys.argv[1]
try:
    rows = json.loads(os.environ["ROWS_JSON"])
except (json.JSONDecodeError, TypeError) as exc:
    print(f"{label} returned invalid rows_json: {exc}", file=sys.stderr)
    raise SystemExit(1)
if not isinstance(rows, list):
    print(f"{label} returned {type(rows).__name__}, expected a row list", file=sys.stderr)
    raise SystemExit(1)
PY
}

assert_rows_empty() {
  local label="$1"
  local rows_json="$2"
  ROWS_JSON="${rows_json}" python3 - "${label}" <<'PY'
import json
import os
import sys

label = sys.argv[1]
try:
    rows = json.loads(os.environ["ROWS_JSON"])
except (json.JSONDecodeError, TypeError) as exc:
    print(f"{label} returned invalid rows_json: {exc}", file=sys.stderr)
    raise SystemExit(1)
if not isinstance(rows, list):
    print(f"{label} returned {type(rows).__name__}, expected a row list", file=sys.stderr)
    raise SystemExit(1)
if rows:
    print(f"{label} returned a row for the hidden share", file=sys.stderr)
    raise SystemExit(1)
PY
}

assert_resource_remote() {
  local kind="$1"
  case "${kind}" in
    database)
      expect_scalar "database" "$(md_scalar "${writer_token}" "" "SELECT count(*)::VARCHAR FROM MD_INFORMATION_SCHEMA.DATABASES WHERE name = $(sql_literal "${database_name}")")" 1
      ;;
    schema)
      expect_scalar "schema" "$(md_scalar "${writer_token}" "${schema_database_name}" "SELECT count(*)::VARCHAR FROM information_schema.schemata WHERE catalog_name = $(sql_literal "${schema_database_name}") AND schema_name = $(sql_literal "${schema_name}")")" 1
      ;;
    table)
      expect_scalar "table" "$(md_scalar "${writer_token}" "${table_database_name}" "SELECT count(*)::VARCHAR FROM information_schema.tables WHERE table_catalog = $(sql_literal "${table_database_name}") AND table_schema = $(sql_literal "${table_schema_name}") AND table_name = $(sql_literal "${table_name}")")" 1
      ;;
    view)
      expect_scalar "view" "$(md_scalar "${writer_token}" "${view_database_name}" "SELECT count(*)::VARCHAR FROM information_schema.views WHERE table_catalog = $(sql_literal "${view_database_name}") AND table_schema = $(sql_literal "${view_schema_name}") AND table_name = $(sql_literal "${view_name}")")" 1
      ;;
    snapshot)
      expect_scalar "snapshot" "$(md_scalar "${writer_token}" "${snapshot_database_name}" "SELECT count(*)::VARCHAR FROM MD_INFORMATION_SCHEMA.DATABASE_SNAPSHOTS WHERE database_name = $(sql_literal "${snapshot_database_name}") AND snapshot_name = $(sql_literal "${snapshot_current_name}")")" 1
      ;;
    share)
      expect_scalar "share" "$(md_scalar "${writer_token}" "" "SELECT count(*)::VARCHAR FROM MD_INFORMATION_SCHEMA.OWNED_SHARES WHERE name = $(sql_literal "${share_name}")")" 1
      ;;
    share_grant)
      expect_scalar "share grant" "$(md_scalar "${MOTHERDUCK_ADMIN_TOKEN}" "" "SELECT count(*)::VARCHAR FROM MD_LIST_SHARE_GRANTEES($(sql_literal "${share_grant_name}")) WHERE lower(grantee_name) = lower($(sql_literal "${share_grant_role_name}")) AND lower(grantee_type) = 'role' AND lower(privilege) = 'read'")" 1
      ;;
    secret)
      expect_scalar "secret" "$(md_scalar "${writer_token}" "" "SELECT count(*)::VARCHAR FROM duckdb_secrets() WHERE name = $(sql_literal "${secret_name}") AND storage = 'motherduck'")" 1
      ;;
    *)
      echo "Unknown resource ${kind}" >&2
      return 1
      ;;
  esac
}

safe_update_resource() {
  local kind="$1"
  local dir="$2"
  case "${kind}" in
    database)
      replace_text "${dir}/resource.tf" 'snapshot_retention_days = 7' 'snapshot_retention_days = 8'
      ;;
    view)
      # The Terraform interpolation belongs to the copied example, not this
      # shell process.
      # shellcheck disable=SC2016
      local new_query='  query    = "SELECT event_id, occurred_at FROM \"${motherduck_database.analytics.name}\".\"${motherduck_schema.mart.name}\".\"${motherduck_table.events.name}\" WHERE event_id IS NOT NULL"'
      NEW_QUERY="${new_query}" perl -0pi -e 's/^\s*query\s*=.*$/$ENV{NEW_QUERY}/m' "${dir}/resource.tf"
      ;;
    snapshot)
      replace_text "${dir}/resource.tf" "\"${snapshot_name}\"" "\"${snapshot_updated_name}\""
      replace_text "${dir}/import.sh" "${snapshot_database_name}.${snapshot_name}" "${snapshot_database_name}.${snapshot_updated_name}"
      snapshot_current_name="${snapshot_updated_name}"
      ;;
    share)
      replace_text "${dir}/resource.tf" "[\"main.${share_table_name}\", \"main.${share_view_name}\"]" "[\"main.${share_table_name}\"]"
      ;;
    secret)
      if [[ "${storage_secret_real}" -eq 0 ]]; then
        replace_text "${dir}/resource.tf" 'region = "us-east-1"' 'region = "us-west-2"'
      fi
      ;;
    schema|table|share_grant)
      # These public resources intentionally have replacement-only or
      # grant-identity fields. The post-apply no-op is their safe lifecycle
      # check; changing them would destroy a useful fixture or alter identity.
      ;;
    *)
      echo "Unknown resource ${kind}" >&2
      return 1
      ;;
  esac
}

expect_noop_plan() {
  local kind="$1"
  local dir="$2"
  local status
  set +e
  run_resource_tf "${kind}" "${dir}" plan -detailed-exitcode -input=false >/dev/null 2>&1
  status=$?
  set -e
  if [[ "${status}" -ne 0 ]]; then
    echo "Expected no-op plan for ${kind}, got exit ${status}" >&2
    return 1
  fi
}

refresh_and_noop_resource() {
  local kind="$1"
  local dir="$2"
  if ! run_resource_tf "${kind}" "${dir}" apply -refresh-only -auto-approve -input=false >/dev/null; then
    echo "Refresh-only apply failed for ${kind}" >&2
    return 1
  fi
  expect_noop_plan "${kind}" "${dir}"
}

repair_share_grant_drift() {
  local dir="${root_test_dir}/resource-share"
  local drift_status
  md_exec "${writer_token}" "" "REVOKE READ ON SHARE $(sql_identifier "${share_name}") FROM USER $(sql_identifier "${reader_username}")"
  set +e
  run_resource_tf share "${dir}" plan -detailed-exitcode -input=false >/dev/null 2>&1
  drift_status=$?
  set -e
  if [[ "${drift_status}" -ne 2 ]]; then
    echo "Expected Terraform to detect revoked share grant drift, got exit ${drift_status}" >&2
    return 1
  fi
  record_coverage "share_grant_remote_drift_detected" "examples/resources/motherduck_share/resource.tf" "passed"
  run_resource_tf share "${dir}" apply -auto-approve -input=false >/dev/null
  expect_noop_plan share "${dir}"
  record_coverage "share_grant_remote_drift_repaired_noop" "examples/resources/motherduck_share/resource.tf" "passed"
}

prepare_secret_variables() {
  secret_key_value="${AWS_ACCESS_KEY_ID:-public-audit-key}"
  secret_value="${AWS_SECRET_ACCESS_KEY:-public-audit-secret}"
  secret_session_value="${AWS_SESSION_TOKEN:-}"
}

import_resource_example() {
  local kind="$1"
  local source_dir="$2"
  local import_dir="${source_dir}-import"
  local token="${writer_token}"
  if [[ "${kind}" == "share_grant" ]]; then
    token="${MOTHERDUCK_ADMIN_TOKEN}"
  fi
  if [[ -e "${import_dir}" ]]; then
    echo "Refusing to reuse import directory ${import_dir}" >&2
    return 1
  fi
  mkdir -p "${import_dir}"
  local file
  for file in "${source_dir}"/*.tf; do
    [[ -f "${file}" ]] || continue
    cp "${file}" "${import_dir}/"
  done
  cp "${source_dir}/import.sh" "${import_dir}/import.sh"
  if [[ "${kind}" == "secret" ]]; then
    # Secret values are write-only. Importing with the public example's
    # params would ask Terraform to replace the imported secret, so the import
    # root intentionally manages only its durable name and type.
    perl -0pi -e 's/\n\s*params\s*=\s*merge\(.*?\n\s*\)//s' "${import_dir}"/*.tf
  fi
  run_resource_tf "${kind}" "${import_dir}" init -backend=false -input=false >/dev/null
  run_resource_tf "${kind}" "${import_dir}" validate >/dev/null
  case "${kind}" in
    database)
      run_import_script "${kind}" "${import_dir}"
      ;;
    schema)
      run_resource_tf "${kind}" "${import_dir}" import -input=false motherduck_database.analytics "${schema_database_name}"
      run_import_script "${kind}" "${import_dir}"
      ;;
    table)
      run_resource_tf "${kind}" "${import_dir}" import -input=false motherduck_database.analytics "${table_database_name}"
      run_resource_tf "${kind}" "${import_dir}" import -input=false motherduck_schema.mart "${table_database_name}.${table_schema_name}"
      run_import_script "${kind}" "${import_dir}"
      ;;
    view)
      run_resource_tf "${kind}" "${import_dir}" import -input=false motherduck_database.analytics "${view_database_name}"
      run_resource_tf "${kind}" "${import_dir}" import -input=false motherduck_schema.mart "${view_database_name}.${view_schema_name}"
      run_resource_tf "${kind}" "${import_dir}" import -input=false motherduck_table.events "${view_database_name}.${view_schema_name}.${view_table_name}"
      run_import_script "${kind}" "${import_dir}"
      ;;
    snapshot)
      run_resource_tf "${kind}" "${import_dir}" import -input=false motherduck_database.analytics "${snapshot_database_name}"
      run_import_script "${kind}" "${import_dir}"
      ;;
    share)
      run_resource_tf "${kind}" "${import_dir}" import -input=false motherduck_database.analytics "${share_database_name}"
      run_resource_tf "${kind}" "${import_dir}" import -input=false motherduck_table.orders "${share_database_name}.main.${share_table_name}"
      run_resource_tf "${kind}" "${import_dir}" import -input=false motherduck_view.daily_revenue "${share_database_name}.main.${share_view_name}"
      run_import_script "${kind}" "${import_dir}"
      run_resource_tf "${kind}" "${import_dir}" import -input=false motherduck_share_grant.reader "${share_name}/user/${reader_username}"
      ;;
    share_grant)
      run_resource_tf "${kind}" "${import_dir}" import -input=false motherduck_database.analytics "${share_grant_database_name}"
      run_resource_tf "${kind}" "${import_dir}" import -input=false motherduck_table.orders "${share_grant_database_name}.main.${share_grant_table_name}"
      run_resource_tf "${kind}" "${import_dir}" import -input=false motherduck_share.analytics "${share_grant_name}"
      run_resource_tf "${kind}" "${import_dir}" import -input=false motherduck_role.analytics_readers "${share_grant_role_name}"
      run_import_script "${kind}" "${import_dir}"
      ;;
    secret)
      run_import_script "${kind}" "${import_dir}"
      ;;
    *)
      echo "Unknown resource ${kind}" >&2
      return 1
      ;;
  esac
  if [[ "${kind}" == "view" || "${kind}" == "share" ]]; then
    # MotherDuck normalizes view definitions on read. Keep the copied
    # example's quoted query and converge once through its normal Update path,
    # which preserves the resource dependency graph for destroy.
    local normalization_status
    set +e
    run_resource_tf "${kind}" "${import_dir}" plan -detailed-exitcode -input=false >/dev/null 2>&1
    normalization_status=$?
    set -e
    if [[ "${normalization_status}" -eq 2 ]]; then
      record_coverage "resource_import_view_normalization_diff" "examples/resources/motherduck_${kind}" "passed"
      run_resource_tf "${kind}" "${import_dir}" apply -auto-approve -input=false >/dev/null
    elif [[ "${normalization_status}" -ne 0 ]]; then
      echo "Expected import plan for ${kind} to be clean or show view normalization, got exit ${normalization_status}" >&2
      return 1
    fi
  fi
  expect_noop_plan "${kind}" "${import_dir}"
  record_coverage "resource_import_noop" "examples/resources/motherduck_${kind}" "passed"
}

run_resource_example() {
  local kind="$1"
  local dir="${root_test_dir}/resource-${kind}"
  copy_actual_example "${kind}" "${dir}"
  # Every resource example creates its own database where needed. The provider
  # must therefore use the default attachment while the resource establishes
  # that database; imported and data-only roots may attach an existing one.
  write_provider "${dir}"
  substitute_resource_example "${kind}" "${dir}"
  if [[ "${kind}" == "share_grant" ]]; then
    register_child "${dir}" "${MOTHERDUCK_ADMIN_TOKEN}"
  else
    register_child "${dir}" "${writer_token}"
  fi
  record_coverage "resource_copy" "examples/resources/motherduck_${kind}" "passed"
  run_resource_tf "${kind}" "${dir}" init -backend=false -input=false >/dev/null
  run_resource_tf "${kind}" "${dir}" validate >/dev/null
  run_resource_tf "${kind}" "${dir}" apply -auto-approve -input=false >/dev/null
  assert_resource_remote "${kind}"
  record_coverage "resource_apply_read_write" "examples/resources/motherduck_${kind}" "passed"

  safe_update_resource "${kind}" "${dir}"
  run_resource_tf "${kind}" "${dir}" apply -auto-approve -input=false >/dev/null
  assert_resource_remote "${kind}"
  refresh_and_noop_resource "${kind}" "${dir}"
  record_coverage "resource_safe_update_refresh_noop" "examples/resources/motherduck_${kind}" "passed"

  import_resource_example "${kind}" "${dir}"
}

write_data_assertion() {
  local kind="$1"
  local dir="$2"
  case "${kind}" in
    current_user|version|live_duckling_size)
      cat >> "${dir}/audit-outputs.tf" <<HCL
output "audit_value" {
  value = data.motherduck_${kind}.current.value
  sensitive = true
}
HCL
      ;;
    databases)
      cat >> "${dir}/audit-outputs.tf" <<HCL
output "audit_rows_json" {
  value = data.motherduck_databases.analytics.rows_json
  sensitive = true
}
HCL
      ;;
    attached_databases)
      cat >> "${dir}/audit-outputs.tf" <<HCL
output "audit_rows_json" {
  value = data.motherduck_attached_databases.current.rows_json
  sensitive = true
}
HCL
      ;;
    database_snapshots)
      cat >> "${dir}/audit-outputs.tf" <<HCL
output "audit_rows_json" {
  value = data.motherduck_database_snapshots.analytics.rows_json
  sensitive = true
}
HCL
      ;;
    owned_shares)
      cat >> "${dir}/audit-outputs.tf" <<HCL
output "audit_rows_json" {
  value = data.motherduck_owned_shares.analytics.rows_json
  sensitive = true
}
HCL
      ;;
    owned_share)
      cat >> "${dir}/audit-outputs.tf" <<HCL
output "audit_value" {
  value = data.motherduck_owned_share.pageviews.name
}
HCL
      ;;
    shared_with_me)
      cat >> "${dir}/audit-outputs.tf" <<HCL
output "audit_rows_json" {
  value = data.motherduck_shared_with_me.analytics.rows_json
  sensitive = true
}
HCL
      ;;
    secrets)
      cat >> "${dir}/audit-outputs.tf" <<HCL
output "audit_rows_json" {
  value = data.motherduck_secrets.analytics.rows_json
  sensitive = true
}
HCL
      ;;
    buckets_for_secret)
      cat >> "${dir}/audit-outputs.tf" <<HCL
output "audit_rows_json" {
  value = data.motherduck_buckets_for_secret.analytics.rows_json
  sensitive = true
}
HCL
      ;;
    files)
      cat >> "${dir}/audit-outputs.tf" <<HCL
output "audit_rows_json" {
  value = data.motherduck_files.landing.rows_json
  sensitive = true
}
HCL
      ;;
    *)
      echo "Unknown data-source ${kind}" >&2
      return 1
      ;;
  esac
}

assert_data_source_result() {
  local kind="$1"
  local dir="$2"
  local token="$3"
  case "${kind}" in
    current_user)
      expect_scalar "current_user" "$(run_tf "${token}" "${dir}" output -raw audit_value)" "${writer_username}" || return 1
      ;;
    version|live_duckling_size)
      expect_nonempty_scalar "${kind}" "$(run_tf "${token}" "${dir}" output -raw audit_value)" || return 1
      ;;
    databases)
      assert_rows_contain "databases" "$(run_tf "${token}" "${dir}" output -raw audit_rows_json)" "${share_database_name}" || return 1
      ;;
    attached_databases)
      assert_rows_contain "attached_databases" "$(run_tf "${token}" "${dir}" output -raw audit_rows_json)" "${share_database_name}" || return 1
      ;;
    database_snapshots)
      assert_rows_contain "database_snapshots" "$(run_tf "${token}" "${dir}" output -raw audit_rows_json)" "${snapshot_updated_name}" || return 1
      ;;
    owned_shares)
      assert_rows_contain "${kind}" "$(run_tf "${token}" "${dir}" output -raw audit_rows_json)" "${share_name}" || return 1
      ;;
    shared_with_me)
      shared_rows="$(run_tf "${token}" "${dir}" output -raw audit_rows_json)"
      if [[ -n "${shared_with_me_expected}" ]]; then
        assert_rows_contain "shared_with_me" "${shared_rows}" "${shared_with_me_expected}" || return 1
      else
        # The intentionally hidden share is URL-grantable but excluded from
        # SHARED_WITH_ME. Keep this negative assertion so an always-empty
        # implementation cannot satisfy the positive discoverable fixture.
        assert_rows_empty "shared_with_me" "${shared_rows}" || return 1
      fi
      ;;
    owned_share)
      expect_scalar "owned_share" "$(run_tf "${token}" "${dir}" output -raw audit_value)" "${share_name}"
      ;;
    secrets)
      assert_rows_contain "secrets" "$(run_tf "${token}" "${dir}" output -raw audit_rows_json)" "${secret_name}" || return 1
      ;;
    buckets_for_secret)
      assert_rows_nonempty "buckets_for_secret" "$(run_tf "${token}" "${dir}" output -raw audit_rows_json)" || return 1
      assert_rows_contain "buckets_for_secret" "$(run_tf "${token}" "${dir}" output -raw audit_rows_json)" "${storage_bucket}" || return 1
      ;;
    files)
      assert_rows_contain "files" "$(run_tf "${token}" "${dir}" output -raw audit_rows_json)" "orders.csv" contains || return 1
      ;;
    *)
      echo "Unknown data-source ${kind}" >&2
      return 1
      ;;
  esac
}

run_data_source_example() {
  local kind="$1"
  local dir_suffix="${2:-}"
  local token="${writer_token}"
  local provider_database=""
  local dir="${root_test_dir}/data-${kind}${dir_suffix}"
  case "${kind}" in
    databases|attached_databases|owned_shares|owned_share|secrets)
      provider_database="${share_database_name}"
      ;;
    database_snapshots)
      provider_database="${snapshot_database_name}"
      ;;
    shared_with_me)
      token="${reader_token}"
      ;;
    current_user|version|live_duckling_size|buckets_for_secret|files)
      ;;
  esac
  if ! copy_actual_data_source "${kind}" "${dir}"; then
    return 1
  fi
  if ! write_provider "${dir}" "${provider_database}"; then
    return 1
  fi
  if ! substitute_data_source_example "${kind}" "${dir}"; then
    return 1
  fi
  if ! write_data_assertion "${kind}" "${dir}"; then
    return 1
  fi
  register_child "${dir}" "${token}"
  if ! run_tf "${token}" "${dir}" init -backend=false -input=false >/dev/null; then
    return 1
  fi
  if ! run_tf "${token}" "${dir}" validate >/dev/null; then
    return 1
  fi
  if ! run_tf "${token}" "${dir}" apply -auto-approve -input=false >/dev/null; then
    return 1
  fi
  assert_data_source_result "${kind}" "${dir}" "${token}" || return 1
  if ! run_tf "${token}" "${dir}" apply -refresh-only -auto-approve -input=false >/dev/null; then
    echo "Refresh-only apply failed for data-source ${kind}" >&2
    return 1
  fi
  expect_noop_data_plan "${kind}" "${dir}" "${token}" || return 1
  record_coverage "data_source_apply_read_refresh_noop" "examples/data-sources/motherduck_${kind}" "passed"
}

expect_noop_data_plan() {
  local kind="$1"
  local dir="$2"
  local token="$3"
  local status
  set +e
  run_tf "${token}" "${dir}" plan -detailed-exitcode -input=false >/dev/null 2>&1
  status=$?
  set -e
  if [[ "${status}" -ne 0 ]]; then
    echo "Expected no-op plan for data-source ${kind}, got exit ${status}" >&2
    return 1
  fi
}

verify_account_http_status() {
  local username="$1"
  local expected_status="$2"
  local stage="$3"
  ACCOUNT_USERNAME="${username}" EXPECTED_STATUS="${expected_status}" MOTHERDUCK_ADMIN_TOKEN="${MOTHERDUCK_ADMIN_TOKEN}" MOTHERDUCK_API_BASE_URL="${MOTHERDUCK_API_BASE_URL:-https://api.motherduck.com}" python3 - "${stage}" <<'PY'
import os
import sys
import urllib.error
import urllib.request

stage = sys.argv[1]
base = os.environ["MOTHERDUCK_API_BASE_URL"].rstrip("/")
username = os.environ["ACCOUNT_USERNAME"]
expected = int(os.environ["EXPECTED_STATUS"])
url = f"{base}/v1/users/{username}/instances"
request = urllib.request.Request(url, headers={"Authorization": "Bearer " + os.environ["MOTHERDUCK_ADMIN_TOKEN"], "Accept": "application/json"})
try:
    with urllib.request.urlopen(request, timeout=30) as response:
        actual = response.status
except urllib.error.HTTPError as error:
    actual = error.code
except urllib.error.URLError as error:
    print(f"{stage} account endpoint failed: {error.reason}", file=sys.stderr)
    raise SystemExit(1)

if actual != expected:
    print(f"{stage} expected HTTP {expected} from the documented instances endpoint, got {actual}", file=sys.stderr)
    raise SystemExit(1)
PY
  record_coverage "account_http_${stage}" "/v1/users/${username}/instances" "HTTP_${expected_status}"
}

bootstrap_account() {
  local dir="$1"
  local username="$2"
  local token_name="$3"
  local reader_mode="${4:-0}"
  mkdir -p "${dir}"
  cp "${ROOT_DIR}/examples/blueprints/writer-bootstrap"/*.tf "${dir}/"
  write_provider "${dir}"
  if [[ "${reader_mode}" -eq 1 ]]; then
    # Keep the recipe's read-write token for one initial share attachment.
    # MotherDuck can require a write-capable session to initialize a reader's
    # workspace; all reader assertions below use the separate read_scaling
    # token minted by this same copied recipe.
    cat >> "${dir}/main.tf" <<HCL

resource "motherduck_access_token" "reader_scaling" {
  username   = motherduck_service_account.writer.username
  name       = "${token_name}-scaling"
  token_type = "read_scaling"
  ttl        = 3600
}
HCL
    cat >> "${dir}/outputs.tf" <<'HCL'

output "reader_scaling_token" {
  value     = motherduck_access_token.reader_scaling.token
  sensitive = true
}
HCL
  fi
  cat > "${dir}/terraform.tfvars" <<HCL
writer_username = "${username}"
writer_token_name = "${token_name}"
writer_token_ttl_seconds = 3600
HCL
  record_coverage "bootstrap_copy" "examples/blueprints/writer-bootstrap" "passed"
  run_tf_admin_sql "${dir}" init -backend=false -input=false >/dev/null
  run_tf_admin_sql "${dir}" validate >/dev/null
  run_tf_admin_sql "${dir}" apply -auto-approve -input=false >/dev/null
}

initialize_storage_probe() {
  if [[ "${storage_secret_real}" -ne 1 ]]; then
    return 0
  fi
  if ! command -v aws >/dev/null 2>&1; then
    storage_unavailable=1
    storage_hard_failure=1
    storage_unavailable_reason="aws is required for the configured storage probe"
    return 0
  fi
  if ! printf 'order_id,amount\ncore-%s,1.00\n' "${suffix}" | aws s3 cp - "${storage_probe_uri}" --only-show-errors >/dev/null 2>&1; then
    storage_unavailable=1
    storage_hard_failure=1
    storage_unavailable_reason="the configured S3 prefix could not accept a run-scoped probe object"
  else
    storage_probe_uploaded=1
  fi
}

cleanup() {
  local status=0
  local child_status
  local i
  if [[ "${KEEP_LIVE_FIXTURE}" == "1" ]]; then
    return 0
  fi

  # Child data-plane states must be destroyed before either bootstrap account.
  # If any child fails, return immediately and retain both bootstrap states and
  # credentials so the operator can retry cleanup with the owning identities.
  for ((i = ${#child_dirs[@]} - 1; i >= 0; i--)); do
    if [[ -d "${child_dirs[$i]}/.terraform" ]]; then
      child_status=0
      if [[ "${child_dirs[$i]}" == "${root_test_dir}/resource-share" && "${discoverable_share_created}" -eq 1 ]]; then
        if md_exec "${writer_token}" "" "DROP SHARE IF EXISTS $(sql_identifier "${discoverable_share_name}")" >/dev/null 2>&1; then
          discoverable_share_created=0
          record_coverage "shared_with_me_positive_fixture_cleanup" "examples/resources/motherduck_share/resource.tf" "passed"
        else
          child_status=$?
        fi
      fi
      if [[ "${child_status}" -ne 0 ]]; then
        status="${child_status}"
        echo "Discoverable share cleanup failed; retaining bootstrap accounts" >&2
        return "${status}"
      fi
      if [[ "${child_dirs[$i]}" == "${root_test_dir}/resource-secret" ]]; then
        run_tf_secret "${child_tokens[$i]}" "${child_dirs[$i]}" destroy -auto-approve -input=false >/dev/null 2>&1 || child_status=$?
      elif [[ "${child_dirs[$i]}" == "${root_test_dir}/resource-share" ]]; then
        run_resource_tf share "${child_dirs[$i]}" destroy -auto-approve -input=false >/dev/null 2>&1 || child_status=$?
      else
        run_tf "${child_tokens[$i]}" "${child_dirs[$i]}" destroy -auto-approve -input=false >/dev/null 2>&1 || child_status=$?
      fi
      if [[ "${child_status}" -ne 0 ]]; then
        status="${child_status}"
        echo "Child cleanup failed; retaining bootstrap accounts for retry: ${child_dirs[$i]}" >&2
        return "${status}"
      fi
    fi
  done

  if [[ "${storage_probe_uploaded}" -eq 1 ]]; then
    if ! aws s3 rm "${storage_probe_uri}" --only-show-errors >/dev/null 2>&1; then
      echo "Unable to remove run-scoped storage probe ${storage_probe_uri}" >&2
      status=1
    else
      record_coverage "storage_probe_cleanup" "${storage_probe_uri}" "passed"
    fi
  fi

  if [[ -d "${bootstrap_reader_dir}/.terraform" ]]; then
    if ! run_tf_admin_sql "${bootstrap_reader_dir}" destroy -auto-approve -input=false >/dev/null 2>&1; then
      status=1
    elif ! verify_account_http_status "${reader_username}" 404 "reader_404"; then
      status=1
    fi
  fi
  if [[ -d "${bootstrap_writer_dir}/.terraform" ]]; then
    if ! run_tf_admin_sql "${bootstrap_writer_dir}" destroy -auto-approve -input=false >/dev/null 2>&1; then
      status=1
    elif ! verify_account_http_status "${writer_username}" 404 "writer_404"; then
      status=1
    fi
  fi
  return "${status}"
}
trap live_cleanup_on_exit EXIT

initialize_storage_probe
prepare_secret_variables

echo "==> Live core example suite (${RUN_ID})"
echo "==> Stage 1: writer and reader bootstrap from examples/blueprints/writer-bootstrap"
bootstrap_account "${bootstrap_writer_dir}" "${writer_username}" "live-core-writer-${suffix}"
writer_token="$(run_tf_admin_sql "${bootstrap_writer_dir}" output -raw writer_token)"
expect_nonempty_scalar writer_token "${writer_token}"
verify_account_http_status "${writer_username}" 200 "writer_200"

bootstrap_account "${bootstrap_reader_dir}" "${reader_username}" "live-core-reader-${suffix}" 1
reader_workspace_token="$(run_tf_admin_sql "${bootstrap_reader_dir}" output -raw writer_token)"
reader_token="$(run_tf_admin_sql "${bootstrap_reader_dir}" output -raw reader_scaling_token)"
expect_nonempty_scalar reader_token "${reader_token}"
verify_account_http_status "${reader_username}" 200 "reader_200"

echo "==> Stage 2: core resource examples under the writer identity"
for resource_kind in database schema table view snapshot secret share; do
  run_resource_example "${resource_kind}"
done

# The standalone role based grant example now includes its database, table,
# share, and role prerequisites. Run that root with the organization admin's
# SQL token so role creation is proved through SQL rather than REST. It uses a
# separate fixture and cannot grant or alter the writer-owned share.
run_resource_example share_grant

echo "==> Stage 3: writer read/write and reader read-only proof"
repair_share_grant_drift
md_exec "${writer_token}" "" "CREATE SHARE $(sql_identifier "${discoverable_share_name}") FROM $(sql_identifier "${share_database_name}") (ACCESS RESTRICTED, VISIBILITY DISCOVERABLE, UPDATE MANUAL)"
discoverable_share_created=1
md_exec "${writer_token}" "" "GRANT READ ON SHARE $(sql_identifier "${discoverable_share_name}") TO USER $(sql_identifier "${reader_username}")"
record_coverage "shared_with_me_positive_fixture" "examples/resources/motherduck_share/resource.tf" "passed"
write_marker="core-${suffix}"
md_exec "${writer_token}" "${share_database_name}" "INSERT INTO $(sql_identifier "${share_database_name}").main.$(sql_identifier "${share_table_name}") (order_id) VALUES ($(sql_literal "${write_marker}"))"
writer_count="$(md_scalar "${writer_token}" "${share_database_name}" "SELECT count(*)::VARCHAR FROM $(sql_identifier "${share_database_name}").main.$(sql_identifier "${share_table_name}") WHERE order_id = $(sql_literal "${write_marker}")")"
expect_scalar "writer_insert_count" "${writer_count}" 1
md_exec "${writer_token}" "" "UPDATE SHARE $(sql_identifier "${share_name}")"
record_coverage "writer_manual_share_refresh" "examples/resources/motherduck_share/resource.tf" "passed"
share_url="$(md_scalar "${writer_token}" "" "SELECT url FROM MD_INFORMATION_SCHEMA.OWNED_SHARES WHERE name = $(sql_literal "${share_name}")")"
expect_nonempty_scalar share_url "${share_url}"
share_attach="${share_url#md:}"
reader_refresh="REFRESH DATABASE $(sql_identifier "${share_database_name}")"
reader_workspace_count="$(md_scalar "${reader_workspace_token}" "${share_attach}" "SELECT count(*)::VARCHAR FROM $(sql_identifier "${share_database_name}").main.$(sql_identifier "${share_table_name}") WHERE order_id = $(sql_literal "${write_marker}")" "${reader_refresh}")"
expect_scalar "reader_workspace_attach_count" "${reader_workspace_count}" 1
record_coverage "reader_read_write_bootstrap_attach" "examples/blueprints/writer-bootstrap" "passed"
reader_count="$(md_scalar "${reader_token}" "${share_attach}" "SELECT count(*)::VARCHAR FROM $(sql_identifier "${share_database_name}").main.$(sql_identifier "${share_table_name}") WHERE order_id = $(sql_literal "${write_marker}")" "${reader_refresh}")"
expect_scalar "reader_shared_read_count" "${reader_count}" 1
if md_exec "${reader_token}" "${share_attach}" "INSERT INTO $(sql_identifier "${share_database_name}").main.$(sql_identifier "${share_table_name}") (order_id) VALUES ($(sql_literal "reader-must-fail-${suffix}"))" "${reader_refresh}" >/dev/null 2>&1; then
  echo "Reader token unexpectedly wrote to the shared fixture" >&2
  exit 1
fi
if md_exec "${reader_token}" "" "GRANT READ ON SHARE $(sql_identifier "${share_name}") TO $(sql_identifier "${reader_username}")" >/dev/null 2>&1; then
  echo "Reader token unexpectedly self-granted share access" >&2
  exit 1
fi
record_coverage "writer_read_write_reader_read_only" "examples/resources/motherduck_share/resource.tf" "passed"

echo "==> Stage 4: catalog data-source examples against live fixtures"
run_data_source_example current_user
run_data_source_example version
run_data_source_example live_duckling_size
run_data_source_example attached_databases
run_data_source_example databases
run_data_source_example database_snapshots
run_data_source_example owned_shares
run_data_source_example owned_share
shared_catalog_name="${discoverable_share_name}"
shared_with_me_expected="${discoverable_share_name}"
run_data_source_example shared_with_me positive
shared_catalog_name="${share_name}"
shared_with_me_expected=""
run_data_source_example shared_with_me hidden
run_data_source_example secrets

if [[ "${storage_secret_real}" -eq 1 ]]; then
  csv_count="$(md_scalar "${writer_token}" "" "SELECT count(*)::VARCHAR FROM read_csv_auto($(sql_literal "${storage_fixture_uri}"))")"
  expect_scalar "orders.csv row count" "${csv_count}" 3
  record_coverage "storage_csv_read" "${storage_fixture_uri}" "passed"
fi

if [[ -n "${storage_fixture_path}" ]]; then
  optional_status=0
  set +e
  run_data_source_example files
  optional_status=$?
  set -e
  if [[ "${optional_status}" -ne 0 ]]; then
    storage_unavailable=1
    if [[ "${storage_secret_real}" -eq 1 ]]; then
      storage_hard_failure=1
    fi
    storage_unavailable_reason="motherduck_files could not list ${storage_fixture_path}"
  fi
else
  echo "Skipping motherduck_files: ${storage_unavailable_reason}" >&2
fi

if [[ "${storage_secret_real}" -eq 1 ]]; then
  optional_status=0
  set +e
  run_data_source_example buckets_for_secret
  optional_status=$?
  set -e
  if [[ "${optional_status}" -ne 0 ]]; then
    storage_unavailable=1
    storage_hard_failure=1
    storage_unavailable_reason="motherduck_buckets_for_secret could not list ${storage_bucket}"
  fi
else
  echo "Skipping motherduck_buckets_for_secret: ${storage_unavailable_reason}" >&2
fi

echo "==> Stage 5: provider example validation"
provider_dir="${root_test_dir}/provider"
mkdir -p "${provider_dir}"
cp "${ROOT_DIR}/examples/provider/provider.tf" "${provider_dir}/provider.tf"
run_tf_provider_example "${provider_dir}" init -backend=false -input=false >/dev/null
run_tf_provider_example "${provider_dir}" validate >/dev/null
run_tf_provider_example "${provider_dir}" plan -input=false >/dev/null
record_coverage "provider_example_validate_plan" "examples/provider" "passed"

echo "==> Coverage matrix: ${coverage_file}"
awk -F '\t' 'NR == 1 || $3 != "passed" || $1 ~ /account_http/ { print }' "${coverage_file}"
if [[ "${storage_hard_failure}" -eq 1 ]]; then
  echo "External storage stage failed with configured credentials: ${storage_unavailable_reason}" >&2
  exit 1
fi
if [[ "${storage_unavailable}" -eq 1 ]]; then
  echo "External storage unavailable: ${storage_unavailable_reason}" >&2
  echo "Core examples completed; returning 42 for the optional storage stage." >&2
  exit 42
fi

echo "Core live example suite passed (${RUN_ID})"
