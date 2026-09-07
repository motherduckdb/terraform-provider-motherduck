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

live_cleanup_on_exit() {
  local original_status=$?
  trap - EXIT

  local cleanup_status=0
  cleanup || cleanup_status=$?
  if [[ "${original_status}" -ne 0 ]]; then
    exit "${original_status}"
  fi
  exit "${cleanup_status}"
}

live_cleanup_mdexec() {
  local operation="$1"
  shift

  local diagnostic
  local status
  if diagnostic="$(go run "${ROOT_DIR}/internal/dev/mdexec" "$@" 2>&1 >/dev/null)"; then
    return 0
  else
    status=$?
  fi

  diagnostic="$(printf '%s\n' "${diagnostic}" | tail -n 1)"
  if [[ -z "${diagnostic}" ]]; then
    diagnostic="command exited with status ${status}"
  fi
  echo "Live cleanup failed (${operation}): ${diagnostic}" >&2
  return "${status}"
}

live_drop_database() {
  local database_name="$1"
  if [[ -n "${database_name}" ]]; then
    live_cleanup_mdexec "drop database ${database_name}" \
      -sql "DROP DATABASE IF EXISTS $(sql_identifier "${database_name}") CASCADE"
  fi
}

live_drop_share() {
  local share_name="$1"
  if [[ -n "${share_name}" ]]; then
    live_cleanup_mdexec "drop share ${share_name}" \
      -sql "DROP SHARE IF EXISTS $(sql_identifier "${share_name}")"
  fi
}

live_drop_secret() {
  local secret_name="$1"
  if [[ -n "${secret_name}" ]]; then
    live_cleanup_mdexec "drop secret ${secret_name}" \
      -sql "DROP SECRET IF EXISTS $(sql_identifier "${secret_name}") FROM motherduck"
  fi
}

live_unname_snapshot() {
  local database_name="$1"
  local snapshot_name="$2"
  if [[ -z "${database_name}" || -z "${snapshot_name}" ]]; then
    return 0
  fi

  local snapshot_id
  local status
  if snapshot_id="$(go run "${ROOT_DIR}/internal/dev/mdexec" -scalar "SELECT coalesce((SELECT snapshot_id::VARCHAR FROM MD_INFORMATION_SCHEMA.DATABASE_SNAPSHOTS WHERE database_name = $(sql_literal "${database_name}") AND snapshot_name = $(sql_literal "${snapshot_name}") ORDER BY created_ts DESC LIMIT 1), '')" 2>/dev/null)"; then
    :
  else
    status=$?
    echo "Live cleanup failed (find snapshot ${snapshot_name} in ${database_name}): mdexec exited with status ${status}" >&2
    return "${status}"
  fi
  if [[ -n "${snapshot_id}" ]]; then
    live_cleanup_mdexec "unname snapshot ${snapshot_name} in ${database_name}" \
      -database "${database_name}" \
      -pre "USE $(sql_identifier "${database_name}")" \
      -sql "ALTER SNAPSHOT $(sql_literal "${snapshot_id}") SET snapshot_name = ''"
  fi
}
