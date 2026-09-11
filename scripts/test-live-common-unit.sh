#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${ROOT_DIR}/scripts/lib/live-common.sh"

test_dir="$(mktemp -d)"
trap 'rm -rf "${test_dir}"' EXIT

stub_bin="${test_dir}/bin"
mkdir -p "${stub_bin}"
cat > "${stub_bin}/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

log_file="${LIVE_COMMON_TEST_LOG:?}"
printf '%s\n' "$*" >> "${log_file}"

if [[ "$*" == *" -scalar "* ]]; then
  if [[ "${LIVE_COMMON_TEST_FAILURE:-}" == "scalar" ]]; then
    echo "stub scalar failure" >&2
    exit 17
  fi
  printf '%s\n' "${LIVE_COMMON_TEST_SCALAR_OUTPUT:-}"
  exit 0
fi

if [[ "$*" == *"DROP DATABASE"* && "${LIVE_COMMON_TEST_FAILURE:-}" == "database" ]]; then
  echo "stub database failure" >&2
  exit 23
fi
if [[ "$*" == *"DROP SHARE"* && "${LIVE_COMMON_TEST_FAILURE:-}" == "share" ]]; then
  echo "stub share failure" >&2
  exit 29
fi
if [[ "$*" == *"DROP SECRET"* && "${LIVE_COMMON_TEST_FAILURE:-}" == "secret" ]]; then
  echo "stub secret failure" >&2
  exit 31
fi
if [[ "$*" == *"ALTER SNAPSHOT"* && "${LIVE_COMMON_TEST_FAILURE:-}" == "update" ]]; then
  echo "stub snapshot update failure" >&2
  exit 37
fi
EOF
chmod +x "${stub_bin}/go"

export PATH="${stub_bin}:${PATH}"
export LIVE_COMMON_TEST_LOG="${test_dir}/go.log"
export LIVE_COMMON_TEST_SCALAR_OUTPUT=""

assert_log_contains() {
  local expected="$1"
  if ! grep -F -- "${expected}" "${LIVE_COMMON_TEST_LOG}" >/dev/null; then
    echo "Expected go invocation to contain: ${expected}" >&2
    cat "${LIVE_COMMON_TEST_LOG}" >&2
    exit 1
  fi
}

# Empty names are intentional no-ops and must not invoke go.
: > "${LIVE_COMMON_TEST_LOG}"
live_drop_database ""
live_drop_share ""
live_drop_secret ""
live_unname_snapshot "" "snapshot"
live_unname_snapshot "database" ""
if [[ -s "${LIVE_COMMON_TEST_LOG}" ]]; then
  echo "Empty cleanup names invoked go" >&2
  exit 1
fi

# Cleanup SQL uses identifier quoting, including embedded double quotes.
live_drop_database 'db"quoted'
live_drop_share 'share"quoted'
live_drop_secret 'secret"quoted'
assert_log_contains 'DROP DATABASE IF EXISTS "db""quoted" CASCADE'
assert_log_contains 'DROP SHARE IF EXISTS "share""quoted"'
assert_log_contains 'DROP SECRET IF EXISTS "secret""quoted" FROM motherduck'

# Every cleanup mutation carries the mdexec allow-prefix guard.
if [[ "$(grep -c -- '-allow-prefix tf ' "${LIVE_COMMON_TEST_LOG}")" -ne 3 ]]; then
  echo "Expected every cleanup mutation to pass -allow-prefix tf" >&2
  cat "${LIVE_COMMON_TEST_LOG}" >&2
  exit 1
fi

# Each direct cleanup operation returns the mdexec failure and emits a short diagnostic.
export LIVE_COMMON_TEST_FAILURE=database
database_status=0
if live_drop_database "db" 2>"${test_dir}/database.stderr"; then
  echo "Expected database cleanup failure" >&2
  exit 1
else
  database_status=$?
fi
if [[ "${database_status}" -ne 23 ]]; then
  echo "Expected database cleanup status 23, got ${database_status}" >&2
  exit 1
fi
grep -F 'Live cleanup failed (drop database db): stub database failure' "${test_dir}/database.stderr" >/dev/null

export LIVE_COMMON_TEST_FAILURE=share
share_status=0
if live_drop_share "share" 2>"${test_dir}/share.stderr"; then
  echo "Expected share cleanup failure" >&2
  exit 1
else
  share_status=$?
fi
if [[ "${share_status}" -ne 29 ]]; then
  echo "Expected share cleanup status 29, got ${share_status}" >&2
  exit 1
fi
grep -F 'Live cleanup failed (drop share share): stub share failure' "${test_dir}/share.stderr" >/dev/null

export LIVE_COMMON_TEST_FAILURE=secret
secret_status=0
if live_drop_secret "secret" 2>"${test_dir}/secret.stderr"; then
  echo "Expected secret cleanup failure" >&2
  exit 1
else
  secret_status=$?
fi
if [[ "${secret_status}" -ne 31 ]]; then
  echo "Expected secret cleanup status 31, got ${secret_status}" >&2
  exit 1
fi
grep -F 'Live cleanup failed (drop secret secret): stub secret failure' "${test_dir}/secret.stderr" >/dev/null

# A missing named snapshot is a successful no-op. A matching row is updated.
unset LIVE_COMMON_TEST_FAILURE
export LIVE_COMMON_TEST_SCALAR_OUTPUT=""
: > "${LIVE_COMMON_TEST_LOG}"
live_unname_snapshot "db'quoted" "snapshot'quoted"
assert_log_contains "WHERE database_name = 'db''quoted' AND snapshot_name = 'snapshot''quoted'"
if grep -F 'ALTER SNAPSHOT' "${LIVE_COMMON_TEST_LOG}" >/dev/null; then
  echo "Empty snapshot lookup attempted an update" >&2
  exit 1
fi

export LIVE_COMMON_TEST_SCALAR_OUTPUT="snapshot-id"
live_unname_snapshot "db" "snapshot"
assert_log_contains "ALTER SNAPSHOT 'snapshot-id' SET snapshot_name = ''"
# The snapshot statement names no object, so the guard target is the database.
assert_log_contains "-allow-prefix tf -database db -allow-target db "

export LIVE_COMMON_TEST_FAILURE=scalar
lookup_status=0
if live_unname_snapshot "db" "snapshot" 2>"${test_dir}/lookup.stderr"; then
  echo "Expected snapshot lookup failure" >&2
  exit 1
else
  lookup_status=$?
fi
if [[ "${lookup_status}" -ne 17 ]]; then
  echo "Expected snapshot lookup status 17, got ${lookup_status}" >&2
  exit 1
fi
grep -F 'Live cleanup failed (find snapshot snapshot in db)' "${test_dir}/lookup.stderr" >/dev/null

unset LIVE_COMMON_TEST_FAILURE
export LIVE_COMMON_TEST_SCALAR_OUTPUT="snapshot-id"
export LIVE_COMMON_TEST_FAILURE=update
update_status=0
if live_unname_snapshot "db" "snapshot" 2>"${test_dir}/update.stderr"; then
  echo "Expected snapshot update failure" >&2
  exit 1
else
  update_status=$?
fi
if [[ "${update_status}" -ne 37 ]]; then
  echo "Expected snapshot update status 37, got ${update_status}" >&2
  exit 1
fi

# EXIT cleanup preserves a pre-existing failure, otherwise returning cleanup failure.
run_exit_case() {
  local expected="$1"
  local original_status="$2"
  local cleanup_status="$3"
  local marker="${test_dir}/cleanup-${original_status}-${cleanup_status}"
  local actual=0
  if ORIGINAL_STATUS="${original_status}" CLEANUP_STATUS="${cleanup_status}" CLEANUP_MARKER="${marker}" \
    bash -c 'source "$1"; cleanup() { : > "${CLEANUP_MARKER}"; return "${CLEANUP_STATUS}"; }; trap live_cleanup_on_exit EXIT; exit "${ORIGINAL_STATUS}"' \
    _ "${ROOT_DIR}/scripts/lib/live-common.sh"; then
    actual=0
  else
    actual=$?
  fi
  if [[ "${actual}" -ne "${expected}" || ! -e "${marker}" ]]; then
    echo "Expected exit status ${expected} with cleanup invocation, got ${actual}" >&2
    exit 1
  fi
}

run_exit_case 0 0 0
run_exit_case 19 0 19
run_exit_case 7 7 19
grep -F 'Live cleanup failed (unname snapshot snapshot in db): stub snapshot update failure' "${test_dir}/update.stderr" >/dev/null

# Exercise the actual writer scenario's cleanup callback without running its
# credentialed setup. Bootstrap credentials must survive a tenant cleanup error.
# Variables and stubs below are used by that dynamically loaded callback.
# shellcheck disable=SC2034,SC2317,SC2329
(
  eval "$(sed -n '/^cleanup() {/,/^}/p' "${ROOT_DIR}/scripts/test-live-blueprint-writer-path.sh")"
  tenant_dir="${test_dir}/tenant"
  bootstrap_dir="${test_dir}/bootstrap"
  mkdir -p "${tenant_dir}/.terraform" "${bootstrap_dir}/.terraform"
  KEEP_LIVE_FIXTURE=0
  writer_token=stub-token
  writer_username=stub-writer
  tenant_share=stub-share
  tenant_database=stub-database
  cli_config=unused
  TERRAFORM_BIN=unused
  RUN_ID=stub
  live_terraform_destroy() {
    echo "$3" >> "${test_dir}/writer-cleanup.log"
    if [[ "$3" == "${tenant_dir}" ]]; then return "${tenant_status}"; fi
  }
  live_drop_share() { return 0; }
  live_drop_database() { return 0; }
  audit_accounts_destroyed() { return 0; }
  tenant_status=23
  status=0
  cleanup > /dev/null 2>&1 || status=$?
  [[ "${status}" == 23 ]]
  [[ "$(cat "${test_dir}/writer-cleanup.log")" == "${tenant_dir}" ]]
  : > "${test_dir}/writer-cleanup.log"
  tenant_status=0
  cleanup
  [[ "$(tail -n 1 "${test_dir}/writer-cleanup.log")" == "${bootstrap_dir}" ]]
)

# Plan assertions preserve command arguments, shell options, and failure codes.
for plan_status in 0 1 2 37; do
  result=0
  PLAN_STATUS="${plan_status}" PLAN_MARKER="${test_dir}/plan-args" \
    bash -e -c '
      source "$1"
      cleanup() { : > "$PLAN_MARKER.cleanup"; }
      trap cleanup EXIT
      expect_noop_plan "expected no drift" bash -c '\''
        printf "%s\\n" "$@" > "$PLAN_MARKER"
        exit "$PLAN_STATUS"
      '\'' _ "argument with spaces" "-var=value"
      [[ $- == *e* ]]
      : > "$PLAN_MARKER.continued"
    ' _ "${ROOT_DIR}/scripts/lib/live-common.sh" 2> "${test_dir}/plan.stderr" || result=$?
  [[ "${result}" == "${plan_status}" ]]
  [[ "$(cat "${test_dir}/plan-args")" == $'argument with spaces\n-var=value' ]]
  [[ -e "${test_dir}/plan-args.cleanup" ]]
  if [[ "${plan_status}" -eq 0 ]]; then
    [[ -e "${test_dir}/plan-args.continued" ]]
    rm "${test_dir}/plan-args.continued"
  else
    [[ ! -e "${test_dir}/plan-args.continued" ]]
  fi
  if [[ "${plan_status}" -eq 2 ]]; then
    [[ "$(cat "${test_dir}/plan.stderr")" == "expected no drift" ]]
  else
    [[ ! -s "${test_dir}/plan.stderr" ]]
  fi
done

for plan_status in 0 1 2 37; do
  result=0
  PLAN_STATUS="${plan_status}" bash -e -c '
    source "$1"
    trap '\''echo cleanup >&2'\'' EXIT
    expect_drift_plan "Expected changes, got " bash -c '\''exit "$PLAN_STATUS"'\''
    echo continued
  ' _ "${ROOT_DIR}/scripts/lib/live-common.sh" > "${test_dir}/drift.stdout" 2> "${test_dir}/drift.stderr" || result=$?
  if [[ "${plan_status}" -eq 2 ]]; then
    [[ "${result}" -eq 0 && "$(cat "${test_dir}/drift.stdout")" == continued ]]
    [[ "$(cat "${test_dir}/drift.stderr")" == cleanup ]]
  else
    [[ "${result}" -eq 1 && ! -s "${test_dir}/drift.stdout" ]]
    [[ "$(cat "${test_dir}/drift.stderr")" == "Expected changes, got ${plan_status}"$'\ncleanup' ]]
  fi
done
