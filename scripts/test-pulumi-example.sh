#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/download-checksum.sh
source "${ROOT_DIR}/scripts/lib/download-checksum.sh"
# shellcheck source=scripts/lib/pulumi-cli.sh
source "${ROOT_DIR}/scripts/lib/pulumi-cli.sh"
RUN_ID="${RUN_ID:-$(date +%Y%m%d%H%M%S)_$$}"
work_dir="${ROOT_DIR}/test-results/pulumi-example-${RUN_ID}"
if [[ -e "${work_dir}" ]]; then echo "Refusing to reuse ${work_dir}" >&2; exit 1; fi
umask 077
mkdir -p "${work_dir}/program/bin"
PULUMI_BIN="$(resolve_pulumi_bin "${work_dir}")"
pulumi_isolated_env "${work_dir}"
cp "${ROOT_DIR}/examples/pulumi/python/"{__main__.py,Pulumi.yaml,requirements.txt} "${work_dir}/program/"
pulumi_use_local_provider "${work_dir}/program/Pulumi.yaml"
go build -o "${work_dir}/program/bin/terraform-provider-motherduck" "${ROOT_DIR}"
cd "${work_dir}/program"
"${PULUMI_BIN}" stack init offline --non-interactive >/dev/null
"${PULUMI_BIN}" config set databaseName pulumi_offline_example --non-interactive
"${PULUMI_BIN}" install --non-interactive
# Preview only. No update is permitted and no real credential is supplied.
MOTHERDUCK_TOKEN=offline-placeholder "${PULUMI_BIN}" preview --non-interactive --diff
printf 'Pulumi SDK generation and no-update preview passed\n'
