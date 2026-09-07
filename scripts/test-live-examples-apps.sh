#!/usr/bin/env bash
set -euo pipefail
umask 077

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/terraform-test.sh
source "${ROOT_DIR}/scripts/lib/terraform-test.sh"
# shellcheck source=scripts/lib/live-common.sh
source "${ROOT_DIR}/scripts/lib/live-common.sh"
isolate_live_test_environment

if [[ -z "${MOTHERDUCK_ADMIN_TOKEN:-}" ]]; then
  echo "MOTHERDUCK_ADMIN_TOKEN is required to provision disposable example accounts" >&2
  exit 1
fi
RUN_ID="${RUN_ID:-$(date +%Y%m%d%H%M%S)_$$}"
PROVIDER_VERSION="${PROVIDER_VERSION:-0.1.1}"
TERRAFORM_BIN="${TERRAFORM_BIN:-terraform}"
require_safe_run_id "${RUN_ID}"
MD_EXAMPLE_DIR="${ROOT_DIR}/test-results/live-examples-apps-${RUN_ID}"
if [[ -e "${MD_EXAMPLE_DIR}" ]]; then
  echo "Refusing to reuse ${MD_EXAMPLE_DIR}" >&2
  exit 1
fi
mkdir -p "${MD_EXAMPLE_DIR}"
# shellcheck disable=SC2034
mirror_dir="${MD_EXAMPLE_DIR}/provider-mirror"
prepare_provider_mirror
TF_CLI_CONFIG_FILE="${MD_EXAMPLE_DIR}/terraformrc"
write_provider_cli_config "${TF_CLI_CONFIG_FILE}"
export RUN_ID PROVIDER_VERSION TERRAFORM_BIN MD_EXAMPLE_DIR TF_CLI_CONFIG_FILE
exec python3 "${ROOT_DIR}/scripts/lib/live-examples-apps.py"
