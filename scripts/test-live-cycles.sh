#!/usr/bin/env bash
set -euo pipefail
umask 077
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/live-common.sh
source "${ROOT_DIR}/scripts/lib/live-common.sh"
RUN_ID="${RUN_ID:-$(date +%Y%m%d%H%M%S)_$$}"
require_safe_run_id "${RUN_ID}"
MD_CYCLE_REPEATS="${MD_CYCLE_REPEATS:-5}"
MD_EXAMPLE_UPDATE_CYCLES="${MD_EXAMPLE_UPDATE_CYCLES:-5}"
for count in "${MD_CYCLE_REPEATS}" "${MD_EXAMPLE_UPDATE_CYCLES}"; do
  if [[ ! "${count}" =~ ^[1-9][0-9]*$ ]]; then
    echo "Cycle counts must be positive integers" >&2
    exit 1
  fi
done
MD_CYCLE_TERRAFORM="$(command -v "${TERRAFORM_BIN:-terraform}")"
MD_CYCLE_REPORT_DIR="${ROOT_DIR}/test-results/lifecycle-cycles-${RUN_ID}"
if [[ -e "${MD_CYCLE_REPORT_DIR}" ]]; then
  echo "Refusing to reuse cycle results: ${MD_CYCLE_REPORT_DIR}" >&2
  exit 1
fi
mkdir -p "${MD_CYCLE_REPORT_DIR}"
TERRAFORM_BIN="${ROOT_DIR}/scripts/terraform-cycle-audit.py"
# Only the explicit disposable-token control may allow replacement.
unset MD_CYCLE_ALLOW_REPLACE
export RUN_ID MD_CYCLE_REPEATS MD_EXAMPLE_UPDATE_CYCLES MD_CYCLE_TERRAFORM MD_CYCLE_REPORT_DIR TERRAFORM_BIN
exec "${ROOT_DIR}/scripts/test-live-examples.sh"
