#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/terraform-test.sh
source "${ROOT_DIR}/scripts/lib/terraform-test.sh"
isolate_offline_test_environment
umask 077

# Every invocation builds the current source once. There is no persistent
# executable cache that could accidentally test an older checkout.
RUN_ID="${RUN_ID:-$(date +%Y%m%d%H%M%S)_$$}"
prepare_provider_mirror
export RUN_ID TF_TEST_PROVIDER_BINARY="${provider_binary}"
result_dir="${ROOT_DIR}/test-results/cli-${RUN_ID}"
mkdir -p "${result_dir}"
for suite in examples import-validation invalid-configuration missing-credentials; do
  echo "==> CLI ${suite}"
  if ! "${ROOT_DIR}/scripts/test-${suite}.sh" > "${result_dir}/${suite}.log" 2>&1; then
    cat "${result_dir}/${suite}.log" >&2
    echo "CLI ${suite} failed. Log: ${result_dir}/${suite}.log" >&2
    exit 1
  fi
  echo "PASS ${suite}"
done
