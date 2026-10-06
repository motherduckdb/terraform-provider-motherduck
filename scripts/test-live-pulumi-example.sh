#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/download-checksum.sh
source "${ROOT_DIR}/scripts/lib/download-checksum.sh"
# shellcheck source=scripts/lib/pulumi-cli.sh
source "${ROOT_DIR}/scripts/lib/pulumi-cli.sh"
: "${MOTHERDUCK_TOKEN:?SQL token required}"
RUN_ID="${RUN_ID:-$(date +%Y%m%d%H%M%S)_$$}"
[[ "${RUN_ID}" =~ ^[a-zA-Z0-9_]+$ ]] || exit 1
work_dir="${ROOT_DIR}/test-results/pulumi-live-${RUN_ID}"
[[ ! -e "${work_dir}" ]] || exit 1
umask 077
mkdir -p "${work_dir}/program/bin"
PULUMI_BIN="$(resolve_pulumi_bin "${work_dir}")"
pulumi_isolated_env "${work_dir}"
cp "${ROOT_DIR}/examples/pulumi/python/"{__main__.py,Pulumi.yaml,requirements.txt} "${work_dir}/program/"
pulumi_use_local_provider "${work_dir}/program/Pulumi.yaml"
go build -o "${work_dir}/program/bin/terraform-provider-motherduck" "${ROOT_DIR}"
cd "${work_dir}/program"
"${PULUMI_BIN}" stack init test --non-interactive >init.log 2>&1
cleanup() {
  local status=$?
  trap - EXIT
  "${PULUMI_BIN}" destroy --yes --non-interactive >destroy.log 2>&1 || status=1
  if [[ "${status}" -ne 0 ]]; then echo "Pulumi test failed, inspect ${work_dir}" >&2; fi
  exit "${status}"
}
trap cleanup EXIT
"${PULUMI_BIN}" config set databaseName "tf_e03_${RUN_ID}" --non-interactive
"${PULUMI_BIN}" install --non-interactive >install.log 2>&1
"${PULUMI_BIN}" preview --non-interactive >preview.log 2>&1
"${PULUMI_BIN}" up --yes --non-interactive >apply.log 2>&1
"${PULUMI_BIN}" refresh --yes --non-interactive >refresh.log 2>&1
"${PULUMI_BIN}" preview --expect-no-changes --non-interactive >noop.log 2>&1
"${PULUMI_BIN}" destroy --yes --non-interactive >destroy.log 2>&1
trap - EXIT
"${PULUMI_BIN}" stack export >state-after-destroy.json
python3 - <<'PY'
import json
s=json.load(open('state-after-destroy.json'))
assert not [r for r in s['deployment'].get('resources', []) if r['type'].startswith('motherduck:')]
PY
result="$(go run "${ROOT_DIR}/internal/dev/mdexec" -scalar "SELECT count(*)::VARCHAR FROM MD_INFORMATION_SCHEMA.DATABASES WHERE name = 'tf_e03_${RUN_ID}'")"
[[ "${result}" == 0 ]]
printf 'PASS: Pulumi build, SDK generation, preview, apply, refresh, no-op preview, destroy and absent database\n'
