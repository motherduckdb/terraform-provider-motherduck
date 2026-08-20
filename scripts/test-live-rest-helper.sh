#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${ROOT_DIR}/scripts/lib/live-common.sh"
source "${ROOT_DIR}/scripts/lib/live-rest.sh"

assert_username() {
  local run_id="$1"
  local pid="$2"
  local got
  got="$(rest_preflight_username "${run_id}" "${pid}")"
  if [[ ! "${got}" =~ ^[A-Za-z][A-Za-z0-9_]*$ ]]; then
    echo "Generated REST preflight username is invalid: ${got}" >&2
    exit 1
  fi
  if [[ "${#got}" -gt 255 ]]; then
    echo "Generated REST preflight username is too long (${#got}): ${got}" >&2
    exit 1
  fi
}

assert_username "20260619191932-tf157" "12345"
assert_username "run.with/slashes and spaces" "12345"
assert_username "" "12345"
assert_username "$(printf 'x%.0s' {1..400})" "12345"

if [[ "$(rest_preflight_username "20260619191932-tf157" "12345")" != "tf_rest_preflight_20260619191932_tf157_12345" ]]; then
  echo "REST preflight username did not sanitize dashes as expected" >&2
  exit 1
fi

require_safe_run_id "20260619191932-tf157"
require_safe_run_id "manual_run_1"

if require_safe_run_id "bad.id" >/dev/null 2>&1; then
  echo "Expected dotted RUN_ID to be rejected for live SQL fixture safety" >&2
  exit 1
fi

if [[ "$(sql_identifier 'a"b')" != '"a""b"' ]]; then
  echo "SQL identifier helper did not double embedded quotes" >&2
  exit 1
fi

if [[ "$(sql_literal "a'b")" != "'a''b'" ]]; then
  echo "SQL literal helper did not double embedded single quotes" >&2
  exit 1
fi
