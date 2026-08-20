#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${ROOT_DIR}/scripts/lib/live-common.sh"

if [[ -n "${RUN_ID:-}" ]]; then
  require_safe_run_id "${RUN_ID}"
fi

if [[ -z "${MOTHERDUCK_TOKEN:-}" ]]; then
  echo "MOTHERDUCK_TOKEN is required for stable SQL live smoke tests" >&2
  exit 1
fi

audit_on_exit() {
  local exit_status=$?
  echo
  echo "==> Auditing live SQL smoke cleanup"
  if ! "${ROOT_DIR}/scripts/audit-live-test-cleanup.sh"; then
    if [[ "${exit_status}" -eq 0 ]]; then
      exit_status=1
    fi
  fi
  exit "${exit_status}"
}
trap audit_on_exit EXIT

run() {
  local label="$1"
  shift
  echo
  echo "==> ${label}"
  "$@"
}

run "canonical values" "${ROOT_DIR}/scripts/test-live-canonical-values.sh"
run "SQL-only blueprint" "${ROOT_DIR}/scripts/test-live-blueprint-sql-only.sh"
run "SQL edge lifecycle" "${ROOT_DIR}/scripts/test-live-sql-edge.sh"
run "SQL import lifecycle" "${ROOT_DIR}/scripts/test-live-sql-import.sh"
run "database options" "${ROOT_DIR}/scripts/test-live-database-options.sh"
run "database drop with unmanaged objects" "${ROOT_DIR}/scripts/test-live-database-drop-with-objects.sh"
run "database drift repair" "${ROOT_DIR}/scripts/test-live-database-drift.sh"
run "SQL object drift repair" "${ROOT_DIR}/scripts/test-live-sql-drift.sh"
run "table replacement" "${ROOT_DIR}/scripts/test-live-table-replace.sh"
run "table type refresh" "${ROOT_DIR}/scripts/test-live-table-types.sh"
run "table replacement with unmanaged dependent view" "${ROOT_DIR}/scripts/test-live-table-unmanaged-view.sh"
run "view drift repair" "${ROOT_DIR}/scripts/test-live-view-drift.sh"
run "DuckLake database lifecycle" "${ROOT_DIR}/scripts/test-live-ducklake-database.sh"
run "provider database attach" "${ROOT_DIR}/scripts/test-live-provider-config.sh"
if [[ -n "${RUN_ID:-}" ]]; then
  run "provider workspace attach" env RUN_ID="${RUN_ID}_workspace" ATTACH_MODE=workspace "${ROOT_DIR}/scripts/test-live-provider-config.sh"
else
  run "provider workspace attach" env ATTACH_MODE=workspace "${ROOT_DIR}/scripts/test-live-provider-config.sh"
fi
run "provider single attach" "${ROOT_DIR}/scripts/test-live-provider-single-attach.sh"
run "quoted identifiers" "${ROOT_DIR}/scripts/test-live-quoted-identifiers.sh"
run "quoted identifier import" "${ROOT_DIR}/scripts/test-live-quoted-identifiers-import.sh"
run "read-only SQL catalog" "${ROOT_DIR}/scripts/test-live-read-only-sql-catalog.sh"
run "raw SQL secret" "${ROOT_DIR}/scripts/test-live-secret-raw-sql.sh"
run "secret metadata drift repair" "${ROOT_DIR}/scripts/test-live-secret-metadata-drift.sh"
run "schema cascade" "${ROOT_DIR}/scripts/test-live-schema-cascade.sh"
run "share modes" "${ROOT_DIR}/scripts/test-live-share-modes.sh"
run "share option drift repair" "${ROOT_DIR}/scripts/test-live-share-option-drift.sh"
run "snapshot drift repair" "${ROOT_DIR}/scripts/test-live-snapshot-drift.sh"
