#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_dir="$(mktemp -d)"
mkdir -p "${test_dir}/groups"
run_id="runner_unit_$$"
result_dir="${ROOT_DIR}/test-results/live-examples-${run_id}"
pass_run_id="${run_id}_pass"
result_dir_pass="${ROOT_DIR}/test-results/live-examples-${pass_run_id}"
trap 'rm -rf "${test_dir}" "${result_dir}" "${result_dir_pass}"' EXIT

for group in core warehouses apps roles; do
  cat >"${test_dir}/groups/test-live-examples-${group}.sh" <<EOF
#!/usr/bin/env bash
set -euo pipefail
printf '%s\\n' '${group}' >> '${test_dir}/called'
EOF
  chmod +x "${test_dir}/groups/test-live-examples-${group}.sh"
done

cat >"${test_dir}/groups/test-live-examples-core.sh" <<EOF
#!/usr/bin/env bash
set -euo pipefail
printf '%s\\n' core >> '${test_dir}/called'
exit 42
EOF
chmod +x "${test_dir}/groups/test-live-examples-core.sh"
cat >"${test_dir}/groups/test-live-examples-warehouses.sh" <<EOF
#!/usr/bin/env bash
set -euo pipefail
printf '%s\\n' warehouses >> '${test_dir}/called'
exit 1
EOF
chmod +x "${test_dir}/groups/test-live-examples-warehouses.sh"

if MOTHERDUCK_TOKEN=stub MOTHERDUCK_ADMIN_TOKEN=stub \
  LIVE_EXAMPLES_SCRIPT_DIR="${test_dir}/groups" \
  RUN_ID="${run_id}" \
  "${ROOT_DIR}/scripts/test-live-examples.sh" >"${test_dir}/failure.log" 2>&1; then
  echo 'Aggregator accepted a failed group' >&2
  exit 1
fi
[[ "$(cat "${test_dir}/called")" == $'core\nwarehouses\napps\nroles' ]]
grep -F 'core         UNAVAILABLE' "${test_dir}/failure.log" >/dev/null
grep -F 'warehouses   FAIL' "${test_dir}/failure.log" >/dev/null
grep -F 'roles        PASS' "${test_dir}/failure.log" >/dev/null

cat >"${test_dir}/groups/test-live-examples-core.sh" <<EOF
#!/usr/bin/env bash
printf '%s\\n' 'skipped=false'
EOF
chmod +x "${test_dir}/groups/test-live-examples-core.sh"
cat >"${test_dir}/groups/test-live-examples-warehouses.sh" <<EOF
#!/usr/bin/env bash
set -euo pipefail
printf '%s\\n' warehouses >> '${test_dir}/called'
EOF
chmod +x "${test_dir}/groups/test-live-examples-warehouses.sh"
if ! MOTHERDUCK_TOKEN=stub MOTHERDUCK_ADMIN_TOKEN=stub \
  LIVE_EXAMPLES_SCRIPT_DIR="${test_dir}/groups" \
  RUN_ID="${pass_run_id}" \
  "${ROOT_DIR}/scripts/test-live-examples.sh" >"${test_dir}/pass.log" 2>&1; then
  echo 'Aggregator rejected a successful group containing skip text' >&2
  exit 1
fi

rm -f "${test_dir}/called"
if env -u MOTHERDUCK_TOKEN MOTHERDUCK_ADMIN_TOKEN=stub LIVE_EXAMPLES_SCRIPT_DIR="${test_dir}/groups" \
  "${ROOT_DIR}/scripts/test-live-examples.sh" >"${test_dir}/missing.log" 2>&1; then
  echo 'Aggregator accepted missing SQL credentials' >&2
  exit 1
fi
[[ ! -e "${test_dir}/called" ]]
grep -F 'MOTHERDUCK_TOKEN is required' "${test_dir}/missing.log" >/dev/null

if env -u MOTHERDUCK_TOKEN -u MOTHERDUCK_ADMIN_TOKEN LIVE_EXAMPLES_SCRIPT_DIR="${test_dir}/groups" \
  "${ROOT_DIR}/scripts/test-live-examples.sh" >"${test_dir}/missing-admin.log" 2>&1; then
  echo 'Aggregator accepted missing admin credentials' >&2
  exit 1
fi
grep -F 'MOTHERDUCK_ADMIN_TOKEN is required' "${test_dir}/missing-admin.log" >/dev/null

echo 'Live example runner tests passed'
