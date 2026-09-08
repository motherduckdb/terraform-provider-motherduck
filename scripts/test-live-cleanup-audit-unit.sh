#!/usr/bin/env bash
set -euo pipefail

# Hermetic checks for the batched reads in scripts/audit-live-test-cleanup.sh.
# The audit now runs its four queries through one mdexec invocation, so the
# result-to-label pairing depends on line position. A clean account returns four
# empty values, which is the case most likely to be mis-parsed.

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

test_dir="$(mktemp -d "${TMPDIR:-/tmp}/mdtf-audittest.XXXXXX")"
trap 'rm -rf "${test_dir}"' EXIT

# Shadow the go toolchain with a PATH stub that replays canned scalar output,
# so these cases run offline and with no MotherDuck account.
run_audit() {
  local canned_format="$1"
  shift
  # Write via printf directly. Capturing this output in a variable first would
  # strip the trailing newlines these cases exist to exercise.
  # shellcheck disable=SC2059
  printf "${canned_format}" > "${test_dir}/canned"
  cat > "${test_dir}/go" <<'STUB'
#!/usr/bin/env bash
# Ignore the go arguments entirely and replay the canned scalar output.
cat "${CANNED_OUTPUT}"
STUB
  chmod +x "${test_dir}/go"
  CANNED_OUTPUT="${test_dir}/canned" \
    MOTHERDUCK_TOKEN="unit-test-token" \
    PATH="${test_dir}:${PATH}" \
    "${ROOT_DIR}/scripts/audit-live-test-cleanup.sh" "$@"
}

# A clean account: four empty results, one per line. Must report four "none"
# labels and succeed, not collapse to zero results.
if ! actual="$(run_audit '\n\n\n\n' 2>&1)"; then
  echo "Expected a clean account to pass the audit, got:" >&2
  printf '%s\n' "${actual}" >&2
  exit 1
fi
expected="$(printf 'databases: none\nowned_shares: none\nsecrets: none\nnamed_snapshots: none')"
if [[ "${actual}" != "${expected}" ]]; then
  echo "Clean-account output mismatch." >&2
  echo "want:" >&2; printf '%s\n' "${expected}" >&2
  echo "got:" >&2; printf '%s\n' "${actual}" >&2
  exit 1
fi

# Leftover objects must fail the audit and name what was found, against the
# right label. Values land on lines two and four.
if actual="$(run_audit '\ntf_share_a, tf_share_b\n\ntf_snap\n' 2>&1)"; then
  echo "Expected leftover objects to fail the audit" >&2
  exit 1
fi
for want in "databases: none" "owned_shares: tf_share_a, tf_share_b" "secrets: none" "named_snapshots: tf_snap"; do
  if ! printf '%s\n' "${actual}" | grep -qxF "${want}"; then
    echo "Expected audit output to contain '${want}', got:" >&2
    printf '%s\n' "${actual}" >&2
    exit 1
  fi
done

# A short read means results and labels no longer line up. Failing loudly beats
# silently reporting an account clean because a query was dropped.
if actual="$(run_audit '\n\n' 2>&1)"; then
  echo "Expected a truncated result set to fail the audit" >&2
  exit 1
fi
if ! printf '%s\n' "${actual}" | grep -q "expected 4 results, got 2"; then
  echo "Expected a result-count diagnostic, got:" >&2
  printf '%s\n' "${actual}" >&2
  exit 1
fi

# More results than queries is equally a pairing bug.
if actual="$(run_audit '\n\n\n\n\n' 2>&1)"; then
  echo "Expected an oversized result set to fail the audit" >&2
  exit 1
fi
if ! printf '%s\n' "${actual}" | grep -q "more results than queries"; then
  echo "Expected an oversized-result diagnostic, got:" >&2
  printf '%s\n' "${actual}" >&2
  exit 1
fi

# The audit must still refuse to run without credentials.
if MOTHERDUCK_TOKEN="" "${ROOT_DIR}/scripts/audit-live-test-cleanup.sh" >/dev/null 2>&1; then
  echo "Expected a missing MOTHERDUCK_TOKEN to fail the audit" >&2
  exit 1
fi

echo "live cleanup audit checks passed"
