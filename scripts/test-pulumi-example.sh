#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PULUMI_BIN="${PULUMI_BIN:-pulumi}"
RUN_ID="${RUN_ID:-$(date +%Y%m%d%H%M%S)_$$}"
work_dir="${ROOT_DIR}/test-results/pulumi-example-${RUN_ID}"
if [[ -e "${work_dir}" ]]; then echo "Refusing to reuse ${work_dir}" >&2; exit 1; fi
umask 077
mkdir -p "${work_dir}/bin" "${work_dir}/backend"
cp "${ROOT_DIR}/examples/pulumi/python/"{__main__.py,Pulumi.yaml,requirements.txt} "${work_dir}/"
go build -o "${work_dir}/bin/terraform-provider-motherduck" "${ROOT_DIR}"
export PULUMI_CONFIG_PASSPHRASE="${PULUMI_CONFIG_PASSPHRASE:-$(openssl rand -hex 32)}"
export PULUMI_BACKEND_URL="file://${work_dir}/backend"
export PULUMI_SKIP_UPDATE_CHECK=true
cd "${work_dir}"
"${PULUMI_BIN}" stack init offline --non-interactive >/dev/null
"${PULUMI_BIN}" config set databaseName pulumi_offline_example --non-interactive
"${PULUMI_BIN}" install --non-interactive
# Preview only. No update is permitted and no real credential is supplied.
MOTHERDUCK_TOKEN=offline-placeholder "${PULUMI_BIN}" preview --non-interactive --diff
printf 'Pulumi SDK generation and no-update preview passed\n'
