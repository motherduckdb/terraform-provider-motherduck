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

python3 - "${ROOT_DIR}/scripts/test-live-examples-core.sh" "${work_dir}/resource-function.sh" <<'PY'
from pathlib import Path
import sys
source = Path(sys.argv[1]).read_text()
body = source.split("run_resource_example() {", 1)[1].split("write_data_assertion() {", 1)[0]
Path(sys.argv[2]).write_text("run_resource_example() {" + body)
PY
# shellcheck disable=SC1091
source "${work_dir}/resource-function.sh"
copy_actual_example() {
  mkdir -p "$2"
  printf '%s' old_identity >"$2/resource.tf"
  printf '%s' old_identity >"$2/import.sh"
}
substitute_resource_example() { :; }
run_resource_tf() { :; }
assert_resource_remote() { :; }
refresh_and_noop_resource() { :; }
safe_update_resource() {
  printf '%s' new_identity >"$2/resource.tf"
  printf '%s' new_identity >"$2/import.sh"
  snapshot_current_name=new_identity
}
replace_text() {
  python3 - "$1" "$2" "$3" <<'PY'
from pathlib import Path
import sys
p = Path(sys.argv[1])
p.write_text(p.read_text().replace(sys.argv[2], sys.argv[3]))
PY
}
import_resource_example() {
  if [[ "$(cat "$2/import.sh")" != "$(cat "$2/resource.tf")" || "${snapshot_updated_name}" != "$(cat "$2/resource.tf")" ]]; then
    echo "Snapshot import or catalog expectation disagrees with final cycle configuration" >&2
    return 1
  fi
}
# Variables are consumed by the sourced resource-cycle function.
# shellcheck disable=SC2034
for cycles in 2 3 4 5; do
  root_test_dir="${work_dir}/snapshot-${cycles}"
  snapshot_name=old_identity
  snapshot_updated_name=new_identity
  snapshot_current_name=old_identity
  MD_EXAMPLE_UPDATE_CYCLES="${cycles}"
  run_resource_example snapshot
done
echo "Even and odd snapshot-cycle import checks passed"
