#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_dir="$(mktemp -d)"
trap 'rm -rf "${test_dir}"' EXIT
mkdir -p "${test_dir}/groups"
run_id="runner_unit_$$"
result_dir="${ROOT_DIR}/test-results/live-examples-${run_id}"
trap 'rm -rf "${test_dir}" "${result_dir}"' EXIT

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

if MOTHERDUCK_TOKEN=stub MOTHERDUCK_ADMIN_TOKEN=stub \
  LIVE_EXAMPLES_SCRIPT_DIR="${test_dir}/groups" \
  RUN_ID="${run_id}" \
  "${ROOT_DIR}/scripts/test-live-examples.sh" >"${test_dir}/failure.log" 2>&1; then
  echo 'Aggregator accepted a failed group' >&2
  exit 1
fi
[[ "$(cat "${test_dir}/called")" == $'core\nwarehouses\napps\nroles' ]]
grep -F 'core         UNAVAILABLE' "${test_dir}/failure.log" >/dev/null
grep -F 'roles        PASS' "${test_dir}/failure.log" >/dev/null

cat >"${test_dir}/groups/test-live-examples-core.sh" <<EOF
#!/usr/bin/env bash
printf '%s\\n' 'skipped=false' >> '${test_dir}/called'
EOF
chmod +x "${test_dir}/groups/test-live-examples-core.sh"
if ! MOTHERDUCK_TOKEN=stub MOTHERDUCK_ADMIN_TOKEN=stub \
  LIVE_EXAMPLES_SCRIPT_DIR="${test_dir}/groups" \
  RUN_ID="${run_id}_pass" \
  "${ROOT_DIR}/scripts/test-live-examples.sh" >"${test_dir}/pass.log" 2>&1; then
  echo 'Aggregator rejected a successful group containing skip text' >&2
  exit 1
fi

rm -f "${test_dir}/called"
if MOTHERDUCK_ADMIN_TOKEN=stub LIVE_EXAMPLES_SCRIPT_DIR="${test_dir}/groups" \
  "${ROOT_DIR}/scripts/test-live-examples.sh" >"${test_dir}/missing.log" 2>&1; then
  echo 'Aggregator accepted missing SQL credentials' >&2
  exit 1
fi
[[ ! -e "${test_dir}/called" ]]
grep -F 'MOTHERDUCK_TOKEN is required' "${test_dir}/missing.log" >/dev/null

echo 'Live example runner tests passed'
