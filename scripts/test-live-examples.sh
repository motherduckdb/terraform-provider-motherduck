#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=./scripts/lib/live-common.sh
source "${ROOT_DIR}/scripts/lib/live-common.sh"
# shellcheck source=./scripts/lib/terraform-test.sh
source "${ROOT_DIR}/scripts/lib/terraform-test.sh"
isolate_live_test_environment

if [[ -z "${MOTHERDUCK_TOKEN:-}" ]]; then
  echo "MOTHERDUCK_TOKEN is required for live example tests." >&2
  exit 1
fi
if [[ -z "${MOTHERDUCK_ADMIN_TOKEN:-}" ]]; then
  echo "MOTHERDUCK_ADMIN_TOKEN is required for live example tests." >&2
  exit 1
fi
RUN_ID="${RUN_ID:-$(date +%Y%m%d%H%M%S)_$$}"
require_safe_run_id "${RUN_ID}"

script_dir="${LIVE_EXAMPLES_SCRIPT_DIR:-${ROOT_DIR}/scripts}"
umask 077
test_dir="${ROOT_DIR}/test-results/live-examples-${RUN_ID}"
if [[ -e "${test_dir}" ]]; then
  echo "Refusing to reuse existing live example results directory: ${test_dir}" >&2
  exit 1
fi
mkdir -p "${test_dir}"
groups=(core warehouses apps roles)
aggregate_status=0
summary_file="${test_dir}/summary"
: >"${summary_file}"

for group in "${groups[@]}"; do
  script="${script_dir}/test-live-examples-${group}.sh"
  log_file="${test_dir}/${group}.log"
  echo "Running live example group: ${group}"
  if [[ ! -x "${script}" ]]; then
    printf '%s\tFAIL\t127\t%s\n' "${group}" "${log_file}" >>"${summary_file}"
    echo "Missing executable live example group: ${script}" >&2
    aggregate_status=1
    continue
  fi

  set +e
  "${script}" >"${log_file}" 2>&1
  group_status=$?
  set -e

  result=PASS
  if [[ "${group_status}" -eq 42 ]]; then
    result=UNAVAILABLE
  elif [[ "${group_status}" -ne 0 ]]; then
    result=FAIL
  fi
  printf '%s\t%s\t%s\t%s\n' "${group}" "${result}" "${group_status}" "${log_file}" >>"${summary_file}"
  if [[ "${result}" != PASS ]]; then
    aggregate_status=1
  fi
  echo "Finished live example group: ${group} (${result}, exit ${group_status})"
done

echo "Live example smoke summary:"
while IFS=$'\t' read -r group result group_status log_file; do
  printf '  %-12s %-13s exit=%s log=%s\n' "${group}" "${result}" "${group_status}" "${log_file}"
done <"${summary_file}"

exit "${aggregate_status}"
