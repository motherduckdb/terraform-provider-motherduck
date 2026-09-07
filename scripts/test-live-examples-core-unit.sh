#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work_dir="$(mktemp -d)"
trap 'rm -rf "${work_dir}"' EXIT

# Exercise the real optional-stage function with failing result/no-op checks.
# Extract definitions only, so this test never bootstraps a live identity.
python3 - "${ROOT_DIR}/scripts/test-live-examples-core.sh" "${work_dir}/functions.sh" <<'PY'
from pathlib import Path
import sys
source = Path(sys.argv[1]).read_text()
chunks = []
for first, following in [("run_data_source_example", "expect_noop_data_plan"), ("assert_rows_empty", "assert_resource_remote")]:
    chunks.append(source.split(first + "() {", 1)[1].split(following + "() {", 1)[0])
Path(sys.argv[2]).write_text("\n".join(first + "() {" + chunk for first, chunk in zip(["run_data_source_example", "assert_rows_empty"], chunks)))
PY
# shellcheck disable=SC1091
source "${work_dir}/functions.sh"

# Used by the sourced live-stage function.
# shellcheck disable=SC2034
writer_token=stub
# shellcheck disable=SC2034
root_test_dir="${work_dir}"
copy_actual_data_source() { :; }
write_provider() { :; }
substitute_data_source_example() { :; }
write_data_assertion() { :; }
register_child() { :; }
run_tf() { :; }
record_coverage() { touch "${work_dir}/recorded"; }

for failure in result noop; do
  assert_data_source_result() { [[ "${failure}" != result ]]; }
  expect_noop_data_plan() { [[ "${failure}" != noop ]]; }
  status=0
  # Optional stages deliberately run with errexit disabled in the live suite.
  set +e
  run_data_source_example files
  status=$?
  set -e
  if [[ "${status}" -eq 0 || -e "${work_dir}/recorded" ]]; then
    echo "Optional core stage swallowed ${failure} failure" >&2
    exit 1
  fi
done
assert_rows_empty hidden '[]'
if assert_rows_empty hidden '[{"name":"unexpected"}]' >/dev/null 2>&1; then
  echo "Hidden-share assertion accepted a nonempty catalog" >&2
  exit 1
fi
echo "Core example failure-propagation checks passed"
