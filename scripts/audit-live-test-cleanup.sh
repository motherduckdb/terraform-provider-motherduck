#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/live-common.sh
source "${ROOT_DIR}/scripts/lib/live-common.sh"

mode="check"
if [[ "${1:-}" == "--sweep" ]]; then
  mode="sweep"
  shift
fi

if [[ "$#" -ne 0 ]]; then
  echo "Usage: $0 [--sweep]" >&2
  exit 2
fi

if [[ -z "${MOTHERDUCK_TOKEN:-}" ]]; then
  echo "MOTHERDUCK_TOKEN is required for live cleanup audits" >&2
  exit 1
fi

found=0

check() {
  local label="$1"
  local query="$2"
  local values
  values="$(go run "${ROOT_DIR}/internal/dev/mdexec" -scalar "${query}")"
  if [[ -n "${values}" ]]; then
    found=1
    echo "${label}: ${values}"
  else
    echo "${label}: none"
  fi
}

check "databases" "SELECT coalesce(string_agg(name, ', ' ORDER BY name), '') FROM MD_INFORMATION_SCHEMA.DATABASES WHERE name LIKE 'tf\\_%' ESCAPE '\\'"
check "owned_shares" "SELECT coalesce(string_agg(name, ', ' ORDER BY name), '') FROM MD_INFORMATION_SCHEMA.OWNED_SHARES WHERE name LIKE 'tf\\_%' ESCAPE '\\'"
check "secrets" "SELECT coalesce(string_agg(name, ', ' ORDER BY name), '') FROM duckdb_secrets() WHERE name LIKE 'tf\\_%' ESCAPE '\\'"
check "named_snapshots" "SELECT coalesce(string_agg(snapshot_name, ', ' ORDER BY snapshot_name), '') FROM MD_INFORMATION_SCHEMA.DATABASE_SNAPSHOTS WHERE snapshot_name LIKE 'tf\\_%' ESCAPE '\\'"

sweep_live_test_objects() {
  sweep_databases
  sweep_shares
  sweep_secrets
  sweep_named_snapshots
}

list_lines() {
  local query="$1"
  go run "${ROOT_DIR}/internal/dev/mdexec" -scalar "${query}"
}

sweep_databases() {
  local names
  names="$(list_lines "SELECT coalesce(string_agg(name, '\n' ORDER BY name), '') FROM MD_INFORMATION_SCHEMA.DATABASES WHERE name LIKE 'tf\\_%' ESCAPE '\\'")"
  while IFS= read -r name; do
    [[ -z "${name}" ]] && continue
    go run "${ROOT_DIR}/internal/dev/mdexec" -allow-prefix "tf_" -sql "DROP DATABASE IF EXISTS $(sql_identifier "${name}") CASCADE"
  done <<<"${names}"
}

sweep_shares() {
  local names
  names="$(list_lines "SELECT coalesce(string_agg(name, '\n' ORDER BY name), '') FROM MD_INFORMATION_SCHEMA.OWNED_SHARES WHERE name LIKE 'tf\\_%' ESCAPE '\\'")"
  while IFS= read -r name; do
    [[ -z "${name}" ]] && continue
    go run "${ROOT_DIR}/internal/dev/mdexec" -allow-prefix "tf_" -sql "DROP SHARE IF EXISTS $(sql_identifier "${name}")"
  done <<<"${names}"
}

sweep_secrets() {
  local names
  names="$(list_lines "SELECT coalesce(string_agg(name, '\n' ORDER BY name), '') FROM duckdb_secrets() WHERE name LIKE 'tf\\_%' ESCAPE '\\'")"
  while IFS= read -r name; do
    [[ -z "${name}" ]] && continue
    go run "${ROOT_DIR}/internal/dev/mdexec" -allow-prefix "tf_" -sql "DROP SECRET IF EXISTS $(sql_identifier "${name}") FROM motherduck"
  done <<<"${names}"
}

sweep_named_snapshots() {
  local rows
  rows="$(list_lines "SELECT coalesce(string_agg(database_name || '\t' || snapshot_id::VARCHAR, '\n' ORDER BY database_name, snapshot_name), '') FROM MD_INFORMATION_SCHEMA.DATABASE_SNAPSHOTS WHERE snapshot_name LIKE 'tf\\_%' ESCAPE '\\'")"
  while IFS=$'\t' read -r database_name snapshot_id; do
    [[ -z "${database_name}" || -z "${snapshot_id}" ]] && continue
    go run "${ROOT_DIR}/internal/dev/mdexec" \
      -database "${database_name}" \
      -pre "USE $(sql_identifier "${database_name}")" \
      -allow-prefix "tf_" \
      -sql "ALTER SNAPSHOT $(sql_literal "${snapshot_id}") SET snapshot_name = '' /* tf_ */"
  done <<<"${rows}"
}

if [[ "${found}" -ne 0 ]]; then
  if [[ "${mode}" == "sweep" ]]; then
    sweep_live_test_objects
    exit 0
  fi
  echo "Live cleanup audit found MotherDuck objects with the test prefix tf_." >&2
  exit 1
fi
