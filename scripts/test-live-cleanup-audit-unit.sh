#!/usr/bin/env bash
set -euo pipefail

# Hermetic checks for isolated scalar reads in the live cleanup audit.
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_dir="$(mktemp -d "${TMPDIR:-/tmp}/mdtf-audittest.XXXXXX")"
trap 'rm -rf "${test_dir}"' EXIT

run_audit() {
  local canned_format="$1"
  shift
  # shellcheck disable=SC2059
  printf "${canned_format}" > "${test_dir}/canned"
  : > "${test_dir}/calls"
  cat > "${test_dir}/go" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
scalar_count=0
for arg in "$@"; do
  if [[ "${arg}" == "-scalar" ]]; then
    scalar_count=$((scalar_count + 1))
  fi
done
if [[ "${scalar_count}" -ne 1 ]]; then
  echo "Each audit read must use a separate invocation." >&2
  exit 1
fi
case "$*" in
  *MD_INFORMATION_SCHEMA.DATABASE_SNAPSHOTS*) label=named_snapshots; row=4 ;;
  *MD_INFORMATION_SCHEMA.OWNED_SHARES*) label=owned_shares; row=2 ;;
  *duckdb_secrets*) label=secrets; row=3 ;;
  *MD_INFORMATION_SCHEMA.DATABASES*) label=databases; row=1 ;;
  *) echo "Unknown audit query" >&2; exit 1 ;;
esac
if [[ "${label}" == "named_snapshots" ]]; then
  if [[ "$*" != *" -timeout 5m "* ]]; then
    echo "Snapshot audit must have a bounded five-minute budget." >&2
    exit 1
  fi
elif [[ "$*" != *" -timeout 2m "* ]]; then
  echo "Other audit reads must retain the two-minute timeout." >&2
  exit 1
fi
printf '%s\n' "${label}" >> "${AUDIT_CALLS}"
if [[ "${AUDIT_STUB_FAIL_LABEL:-}" == "${label}" ]]; then
  echo "stub query failure" >&2
  exit 1
fi
# Extra canned rows belong to the final read, allowing malformed-output tests.
if [[ "${row}" -eq 4 ]]; then
  sed -n '4,$p' "${CANNED_OUTPUT}"
else
  sed -n "${row}p" "${CANNED_OUTPUT}"
fi
STUB
  chmod +x "${test_dir}/go"
  CANNED_OUTPUT="${test_dir}/canned" AUDIT_CALLS="${test_dir}/calls" \
    MOTHERDUCK_TOKEN="unit-test-token" PATH="${test_dir}:${PATH}" \
    "${ROOT_DIR}/scripts/audit-live-test-cleanup.sh" "$@"
}

if ! actual="$(run_audit '\n\n\n\n' 2>&1)"; then
  echo "Expected a clean account to pass the audit, got:" >&2
  printf '%s\n' "${actual}" >&2
  exit 1
fi
expected="$(printf 'databases: none\nowned_shares: none\nsecrets: none\nnamed_snapshots: none')"
if [[ "${actual}" != "${expected}" ]]; then
  echo "Clean-account output mismatch." >&2
  printf '%s\n' "${actual}" >&2
  exit 1
fi
if [[ "$(cat "${test_dir}/calls")" != "$(printf 'databases\nowned_shares\nsecrets\nnamed_snapshots')" ]]; then
  echo "Expected all four reads, in order, on separate invocations." >&2
  exit 1
fi

if actual="$(run_audit '\ntf_share_a, tf_share_b\n\ntf_snap\n' 2>&1)"; then
  echo "Expected leftover objects to fail the audit" >&2
  exit 1
fi
for want in "databases: none" "owned_shares: tf_share_a, tf_share_b" "secrets: none" "named_snapshots: tf_snap"; do
  if ! printf '%s\n' "${actual}" | grep -qxF "${want}"; then
    echo "Expected audit output to contain '${want}'" >&2
    exit 1
  fi
done

if actual="$(run_audit '\n\n' 2>&1)"; then
  echo "Expected a truncated result set to fail the audit" >&2
  exit 1
fi
if ! printf '%s\n' "${actual}" | grep -q 'secrets expected 1 result, got 0'; then
  echo "Expected the missing scalar result to be identified" >&2
  exit 1
fi

if actual="$(run_audit '\n\n\n\n\n' 2>&1)"; then
  echo "Expected an oversized result set to fail the audit" >&2
  exit 1
fi
if ! printf '%s\n' "${actual}" | grep -q 'named_snapshots expected 1 result, got 2'; then
  echo "Expected the oversized scalar result to be identified" >&2
  exit 1
fi

for label in databases owned_shares secrets named_snapshots; do
  if actual="$(AUDIT_STUB_FAIL_LABEL="${label}" run_audit '\n\n\n\n' 2>&1)"; then
    echo "Expected a failed ${label} read to fail the audit" >&2
    exit 1
  fi
  if ! printf '%s\n' "${actual}" | grep -q "failed while reading ${label}"; then
    echo "Expected the failed read to be identified" >&2
    exit 1
  fi
  if printf '%s\n' "${actual}" | grep -q "^${label}: none$"; then
    echo "A failed read must not be reported as clean" >&2
    exit 1
  fi
done

if MOTHERDUCK_TOKEN="" "${ROOT_DIR}/scripts/audit-live-test-cleanup.sh" >/dev/null 2>&1; then
  echo "Expected a missing MOTHERDUCK_TOKEN to fail the audit" >&2
  exit 1
fi

# Sweep mode must split multi-row listings into one statement per object,
# unname snapshots before dropping databases, and audit again afterwards.
cat > "${test_dir}/go" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
args="$*"
if [[ "${args}" == *" -sql "* ]]; then
  for ((i = 1; i <= $#; i++)); do
    if [[ "${!i}" == "-sql" ]]; then
      next=$((i + 1))
      printf '%s\n' "${!next}" >> "${SWEEP_CALLS}"
    fi
  done
  exit 0
fi
swept=0
if [[ -s "${SWEEP_CALLS}" && "${SWEEP_LEAVE_OBJECTS:-}" != "1" ]]; then
  swept=1
fi
if [[ "${args}" == *" -timeout "* ]]; then
  if [[ "${swept}" -eq 0 && "${args}" == *"MD_INFORMATION_SCHEMA.DATABASES"* ]]; then
    echo "tf_a, tf_b"
  elif [[ "${swept}" -eq 0 && "${args}" == *"DATABASE_SNAPSHOTS"* ]]; then
    echo "tf_snap"
  else
    echo
  fi
  exit 0
fi
if [[ "${args}" == *"'\n'"* || "${args}" == *"'\t'"* ]]; then
  echo "Sweep listings must not rely on backslash escapes, which DuckDB keeps literally." >&2
  exit 1
fi
case "${args}" in
  *MD_INFORMATION_SCHEMA.DATABASES*) printf 'tf_a\ntf_b\n' ;;
  *DATABASE_SNAPSHOTS*) printf 'tf_a\tsnap-1\ttf_snap\n' ;;
  *) echo ;;
esac
STUB
chmod +x "${test_dir}/go"
: > "${test_dir}/sweep_calls"
if ! actual="$(SWEEP_CALLS="${test_dir}/sweep_calls" MOTHERDUCK_TOKEN="unit-test-token" PATH="${test_dir}:${PATH}" \
  "${ROOT_DIR}/scripts/audit-live-test-cleanup.sh" --sweep 2>&1)"; then
  echo "Expected a successful sweep to pass, got:" >&2
  printf '%s\n' "${actual}" >&2
  exit 1
fi
expected_calls="$(printf '%s\n' \
  "ALTER SNAPSHOT 'snap-1' SET snapshot_name = ''" \
  'DROP DATABASE IF EXISTS "tf_a" CASCADE' \
  'DROP DATABASE IF EXISTS "tf_b" CASCADE')"
if [[ "$(cat "${test_dir}/sweep_calls")" != "${expected_calls}" ]]; then
  echo "Unexpected sweep statements:" >&2
  cat "${test_dir}/sweep_calls" >&2
  exit 1
fi

: > "${test_dir}/sweep_calls"
if actual="$(SWEEP_LEAVE_OBJECTS=1 SWEEP_CALLS="${test_dir}/sweep_calls" MOTHERDUCK_TOKEN="unit-test-token" PATH="${test_dir}:${PATH}" \
  "${ROOT_DIR}/scripts/audit-live-test-cleanup.sh" --sweep 2>&1)"; then
  echo "Expected a sweep that leaves objects behind to fail" >&2
  exit 1
fi
if ! printf '%s\n' "${actual}" | grep -q 'sweep left MotherDuck objects'; then
  echo "Expected the incomplete sweep to be reported" >&2
  exit 1
fi

echo "live cleanup audit checks passed"
