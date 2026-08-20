#!/usr/bin/env bash

require_safe_run_id() {
  local run_id="$1"

  if [[ -z "${run_id}" ]]; then
    echo "RUN_ID must not be empty." >&2
    return 1
  fi

  if [[ ! "${run_id}" =~ ^[A-Za-z0-9_-]+$ ]]; then
    echo "RUN_ID must contain only letters, numbers, underscores, or hyphens." >&2
    echo "Live smoke fixtures use RUN_ID in SQL object names after normalizing hyphens; dots, spaces, slashes, and other punctuation are not safe for Terraform import IDs." >&2
    echo "Got RUN_ID=${run_id}" >&2
    return 1
  fi
}

sql_identifier() {
  local value="$1"
  printf '"%s"' "${value//\"/\"\"}"
}

sql_literal() {
  local value="$1"
  local escaped
  escaped="$(printf '%s' "${value}" | sed "s/'/''/g")"
  printf "'%s'" "${escaped}"
}

live_terraform_destroy() {
  local cli_config="$1"
  local terraform_bin="$2"
  local work_dir="$3"
  shift 3

  if [[ -d "${work_dir}/.terraform" ]]; then
    TF_CLI_CONFIG_FILE="${cli_config}" "${terraform_bin}" -chdir="${work_dir}" destroy -auto-approve -input=false "$@"
  fi
}

live_drop_database() {
  local database_name="$1"
  if [[ -n "${database_name}" ]]; then
    go run "${ROOT_DIR}/internal/dev/mdexec" -sql "DROP DATABASE IF EXISTS $(sql_identifier "${database_name}") CASCADE" >/dev/null 2>&1 || true
  fi
}

live_drop_share() {
  local share_name="$1"
  if [[ -n "${share_name}" ]]; then
    go run "${ROOT_DIR}/internal/dev/mdexec" -sql "DROP SHARE IF EXISTS $(sql_identifier "${share_name}")" >/dev/null 2>&1 || true
  fi
}

live_drop_secret() {
  local secret_name="$1"
  if [[ -n "${secret_name}" ]]; then
    go run "${ROOT_DIR}/internal/dev/mdexec" -sql "DROP SECRET IF EXISTS $(sql_identifier "${secret_name}") FROM motherduck" >/dev/null 2>&1 || true
  fi
}

live_unname_snapshot() {
  local database_name="$1"
  local snapshot_name="$2"
  if [[ -z "${database_name}" || -z "${snapshot_name}" ]]; then
    return 0
  fi

  local snapshot_id
  snapshot_id="$(go run "${ROOT_DIR}/internal/dev/mdexec" -database "${database_name}" -scalar "SELECT coalesce((SELECT snapshot_id::VARCHAR FROM MD_INFORMATION_SCHEMA.DATABASE_SNAPSHOTS WHERE database_name = $(sql_literal "${database_name}") AND snapshot_name = $(sql_literal "${snapshot_name}") ORDER BY created_ts DESC LIMIT 1), '')" 2>/dev/null || true)"
  if [[ -n "${snapshot_id}" ]]; then
    go run "${ROOT_DIR}/internal/dev/mdexec" -database "${database_name}" -pre "USE $(sql_identifier "${database_name}")" -sql "ALTER SNAPSHOT $(sql_literal "${snapshot_id}") SET snapshot_name = ''" >/dev/null 2>&1 || true
  fi
}
