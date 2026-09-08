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

# Every mdexec process pays the MotherDuck boot sequence: an extension install,
# a token round trip and a duckling start. Running the four audit reads as one
# invocation pays it once instead of four times, which is the difference
# between a fast audit and one that can exhaust the client boot timeout.
audit_labels=(databases owned_shares secrets named_snapshots)
audit_queries=(
  "SELECT coalesce(string_agg(name, ', ' ORDER BY name), '') FROM MD_INFORMATION_SCHEMA.DATABASES WHERE name LIKE 'tf\\_%' ESCAPE '\\'"
  "SELECT coalesce(string_agg(name, ', ' ORDER BY name), '') FROM MD_INFORMATION_SCHEMA.OWNED_SHARES WHERE name LIKE 'tf\\_%' ESCAPE '\\'"
  "SELECT coalesce(string_agg(name, ', ' ORDER BY name), '') FROM duckdb_secrets() WHERE name LIKE 'tf\\_%' ESCAPE '\\'"
  "SELECT coalesce(string_agg(snapshot_name, ', ' ORDER BY snapshot_name), '') FROM MD_INFORMATION_SCHEMA.DATABASE_SNAPSHOTS WHERE snapshot_name LIKE 'tf\\_%' ESCAPE '\\'"
)

scalar_args=()
for query in "${audit_queries[@]}"; do
  scalar_args+=(-scalar "${query}")
done

# Read through a file rather than a command substitution: a clean account
# returns four empty values, and $(...) strips the trailing newlines that
# distinguish four empty results from none at all.
audit_output="$(mktemp "${TMPDIR:-/tmp}/mdtf-audit.XXXXXX")"
trap 'rm -f "${audit_output}"' EXIT
go run "${ROOT_DIR}/internal/dev/mdexec" "${scalar_args[@]}" > "${audit_output}"

audit_index=0
while IFS= read -r values; do
  label="${audit_labels[${audit_index}]:-}"
  if [[ -z "${label}" ]]; then
    echo "Live cleanup audit returned more results than queries." >&2
    exit 1
  fi
  audit_index=$((audit_index + 1))
  if [[ -n "${values}" ]]; then
    found=1
    echo "${label}: ${values}"
  else
    echo "${label}: none"
  fi
done < "${audit_output}"

if [[ "${audit_index}" -ne "${#audit_labels[@]}" ]]; then
  echo "Live cleanup audit expected ${#audit_labels[@]} results, got ${audit_index}." >&2
  exit 1
fi

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
  rows="$(list_lines "SELECT coalesce(string_agg(database_name || '\t' || snapshot_id::VARCHAR || '\t' || snapshot_name, '\n' ORDER BY database_name, snapshot_name), '') FROM MD_INFORMATION_SCHEMA.DATABASE_SNAPSHOTS WHERE snapshot_name LIKE 'tf\\_%' ESCAPE '\\'")"
  while IFS=$'\t' read -r database_name snapshot_id snapshot_name; do
    [[ -z "${database_name}" || -z "${snapshot_id}" || -z "${snapshot_name}" ]] && continue
    # ALTER SNAPSHOT addresses the snapshot by id, so pass the prefixed
    # snapshot name as the guard's target instead of a comment.
    go run "${ROOT_DIR}/internal/dev/mdexec" \
      -database "${database_name}" \
      -pre "USE $(sql_identifier "${database_name}")" \
      -allow-prefix "tf_" \
      -allow-target "${snapshot_name}" \
      -sql "ALTER SNAPSHOT $(sql_literal "${snapshot_id}") SET snapshot_name = ''"
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
