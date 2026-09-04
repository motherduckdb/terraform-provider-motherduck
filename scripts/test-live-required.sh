#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ -z "${MOTHERDUCK_TOKEN:-}" ]]; then
  echo "MOTHERDUCK_TOKEN is required for the live contract gate." >&2
  exit 1
fi
audit_on_exit() {
  local exit_status=$?
  if ! "${ROOT_DIR}/scripts/audit-live-test-cleanup.sh"; then
    if [[ "${exit_status}" -eq 0 ]]; then
      exit_status=1
    fi
  fi
  exit "${exit_status}"
}
trap audit_on_exit EXIT

cd "${ROOT_DIR}"
MD_TF_ACC=1 go test -tags=acceptance -count=1 ./internal/client/sql
TF_ACC=1 MD_TF_ACC=1 go test -tags=acceptance -count=1 ./internal/acceptance
