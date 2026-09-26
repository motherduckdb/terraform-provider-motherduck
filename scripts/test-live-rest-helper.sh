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

# The admin token must reach curl on stdin, never in its arguments.
stub_dir="$(mktemp -d)"
trap 'rm -rf "${stub_dir}"' EXIT
mkdir -p "${stub_dir}/bin" "${stub_dir}/root"
cat >"${stub_dir}/bin/curl" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${STUB_LOG_DIR}/argv"
cat >>"${STUB_LOG_DIR}/stdin"
output_file=""
while [[ "$#" -gt 0 ]]; do
  case "$1" in
    -o) output_file="$2"; shift 2 ;;
    *) shift ;;
  esac
done
if [[ -n "${output_file}" && "${output_file}" != /dev/null ]]; then
  printf '{}\n' >"${output_file}"
fi
printf '201'
STUB
chmod +x "${stub_dir}/bin/curl"
(
  ROOT_DIR="${stub_dir}/root"
  PATH="${stub_dir}/bin:${PATH}"
  export STUB_LOG_DIR="${stub_dir}"
  MOTHERDUCK_ADMIN_TOKEN="stub-admin-secret-value"
  MOTHERDUCK_API_BASE_URL="http://stub.invalid"
  RUN_ID="helper_unit"
  preflight_rest_admin
)
if grep -F 'stub-admin-secret-value' "${stub_dir}/argv" >/dev/null; then
  echo "REST admin preflight passed the admin token in curl arguments" >&2
  exit 1
fi
if [[ "$(grep -cF 'Authorization: Bearer stub-admin-secret-value' "${stub_dir}/stdin")" -ne 2 ]]; then
  echo "REST admin preflight did not send the admin token on stdin for both requests" >&2
  exit 1
fi
if ! grep -F -- '-H @-' "${stub_dir}/argv" >/dev/null; then
  echo "REST admin preflight did not ask curl to read headers from stdin" >&2
  exit 1
fi
