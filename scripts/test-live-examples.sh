#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ -z "${MOTHERDUCK_TOKEN:-}" ]]; then
  echo "MOTHERDUCK_TOKEN is required for live example tests." >&2
  exit 1
fi
if [[ -z "${MOTHERDUCK_ADMIN_TOKEN:-}" ]]; then
  echo "MOTHERDUCK_ADMIN_TOKEN is required for live example tests." >&2
  exit 1
fi

script_dir="${LIVE_EXAMPLES_SCRIPT_DIR:-${ROOT_DIR}/scripts}"
test_dir="$(mktemp -d)"
trap 'rm -rf "${test_dir}"' EXIT
groups=(core warehouses apps roles)
aggregate_status=0
summary_file="${test_dir}/summary"
: >"${summary_file}"

for group in "${groups[@]}"; do
  script="${script_dir}/test-live-examples-${group}.sh"
  log_file="${test_dir}/${group}.log"
  if [[ ! -x "${script}" ]]; then
    printf '%s\tFAIL\n' "${group}" >>"${summary_file}"
    echo "Missing executable live example group: ${script}" >&2
    aggregate_status=1
    continue
  fi

  set +e
  "${script}" >"${log_file}" 2>&1
  group_status=$?
  set -e

  result=PASS
  if [[ "${group_status}" -ne 0 ]]; then
    result=FAIL
  elif grep -Eqi '(^|[^[:alpha:]])skip(ped|ping)?([^[:alpha:]]|$)' "${log_file}"; then
    result=SKIP
  fi
  printf '%s\t%s\n' "${group}" "${result}" >>"${summary_file}"
  if [[ "${result}" != PASS ]]; then
    aggregate_status=1
    echo "--- ${group} (${result}) ---" >&2
    sed -n '1,20p' "${log_file}" >&2
  fi
done

echo "Live example smoke summary:"
while IFS=$'\t' read -r group result; do
  printf '  %-12s %s\n' "${group}" "${result}"
done <"${summary_file}"

exit "${aggregate_status}"
