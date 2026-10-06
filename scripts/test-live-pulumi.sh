#!/usr/bin/env bash
# Live Pulumi lifecycle through the Any Terraform Provider bridge, using the
# provider built from this checkout. The SQL program covers every SQL-backed
# resource. The admin program runs when MOTHERDUCK_ADMIN_TOKEN is set.
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/download-checksum.sh
source "${ROOT_DIR}/scripts/lib/download-checksum.sh"
# shellcheck source=scripts/lib/pulumi-cli.sh
source "${ROOT_DIR}/scripts/lib/pulumi-cli.sh"
# shellcheck source=scripts/lib/live-common.sh
source "${ROOT_DIR}/scripts/lib/live-common.sh"
: "${MOTHERDUCK_TOKEN:?MOTHERDUCK_TOKEN is required for the live Pulumi lifecycle}"
RUN_ID="${RUN_ID:-$(date +%Y%m%d%H%M%S)_$$}"
require_safe_run_id "${RUN_ID}"
work_dir="${ROOT_DIR}/test-results/pulumi-lifecycle-${RUN_ID}"
if [[ -e "${work_dir}" ]]; then echo "Refusing to reuse ${work_dir}" >&2; exit 1; fi
umask 077
mkdir -p "${work_dir}"
PULUMI_BIN="$(resolve_pulumi_bin "${work_dir}")"
pulumi_isolated_env "${work_dir}"
go build -o "${work_dir}/terraform-provider-motherduck" "${ROOT_DIR}"

sql_active=false
admin_active=false
cleanup() {
  local status=$?
  trap - EXIT
  local program
  local -a programs=()
  [[ "${sql_active}" == "true" ]] && programs+=(sql)
  [[ "${admin_active}" == "true" ]] && programs+=(admin)
  for program in ${programs[@]+"${programs[@]}"}; do
    if ! (cd "${work_dir}/${program}" && "${PULUMI_BIN}" destroy --yes --non-interactive > destroy-on-exit.log 2>&1); then
      echo "Pulumi ${program} cleanup failed, inspect ${work_dir}/${program}" >&2
      status=1
    fi
  done
  if [[ "${status}" -ne 0 ]]; then
    echo "Pulumi lifecycle failed. Logs are in ${work_dir}" >&2
  fi
  exit "${status}"
}
trap cleanup EXIT

# prepare_program <fixture> copies a Pulumi program, points it at the local
# provider, and creates its stack.
prepare_program() {
  local fixture="$1"
  local dir="${work_dir}/${fixture}"
  mkdir -p "${dir}/bin"
  cp "${ROOT_DIR}/test-fixtures/pulumi-live/${fixture}/Pulumi.yaml" "${dir}/Pulumi.yaml"
  pulumi_use_local_provider "${dir}/Pulumi.yaml"
  cp "${work_dir}/terraform-provider-motherduck" "${dir}/bin/terraform-provider-motherduck"
  (
    cd "${dir}"
    "${PULUMI_BIN}" stack init live --non-interactive > stack-init.log 2>&1
    # A leading letter keeps Pulumi YAML from reading an all-digit run ID,
    # where underscores are digit separators, as a number.
    "${PULUMI_BIN}" config set --plaintext runId "r${RUN_ID}" --non-interactive
    "${PULUMI_BIN}" install --non-interactive > install.log 2>&1
  )
}

# pulumi_in <fixture> <args...> runs Pulumi in a program directory.
pulumi_in() {
  local fixture="$1"
  shift
  (cd "${work_dir}/${fixture}" && "${PULUMI_BIN}" "$@")
}

configure() {
  local fixture="$1"
  shift
  while [[ "$#" -gt 0 ]]; do
    pulumi_in "${fixture}" config set --plaintext "$1" "$2" --non-interactive
    shift 2
  done
}

# converge <fixture> <label> applies, refreshes, and requires an empty preview,
# which is the no-drift contract Terraform users get from a no-op plan.
converge() {
  local fixture="$1"
  local label="$2"
  pulumi_in "${fixture}" up --yes --non-interactive --skip-preview > "${work_dir}/${fixture}/up-${label}.log" 2>&1
  pulumi_in "${fixture}" refresh --yes --non-interactive > "${work_dir}/${fixture}/refresh-${label}.log" 2>&1
  pulumi_in "${fixture}" preview --expect-no-changes --non-interactive --diff > "${work_dir}/${fixture}/noop-${label}.log" 2>&1
}

output() {
  pulumi_in "$1" stack output "$2" --non-interactive
}

expect_output() {
  local fixture="$1" name="$2" want="$3" got
  got="$(output "${fixture}" "${name}")"
  if [[ "${got}" != "${want}" ]]; then
    echo "Pulumi ${fixture} output ${name} is '${got}', expected '${want}'" >&2
    exit 1
  fi
}

mdexec_scalar() {
  go run "${ROOT_DIR}/internal/dev/mdexec" -scalar "$1"
}

database="tf_pulumi_r${RUN_ID}"

echo "==> Pulumi SQL lifecycle (${RUN_ID})"
sql_active=true
prepare_program sql
configure sql viewLimit 1 tableLabelType VARCHAR secretScope s3://tf-pulumi-a/ flightRuntime 300 guideBody v1
converge sql initial
expect_output sql databaseName "${database}"
expect_output sql flightInstanceType F4
expect_output sql flightRuntime 300
auto_name="$(output sql autoDatabaseName)"
if [[ ! "${auto_name}" =~ ^tf_pulumi_auto-[a-z0-9]{7}$ ]]; then
  echo "Expected a Pulumi auto-name for the unnamed database, got '${auto_name}'" >&2
  exit 1
fi

# In-place updates, plus a column change that replaces the table under the
# same name through deleteBeforeReplace.
configure sql viewLimit 2 tableLabelType INTEGER secretScope s3://tf-pulumi-b/ flightRuntime 600 guideBody v2
converge sql updated
expect_output sql flightRuntime 600
expect_output sql guideVersion 2
if ! grep -q 'deleted original' "${work_dir}/sql/up-updated.log"; then
  echo "Expected the table replacement to delete the original first" >&2
  exit 1
fi
label_type="$(mdexec_scalar "SELECT data_type FROM information_schema.columns WHERE table_catalog = '${database}' AND table_schema = 'app' AND table_name = 'facts' AND column_name = 'label'")"
if [[ "${label_type}" != "INTEGER" ]]; then
  echo "Expected the replaced table to have an INTEGER label column, got '${label_type}'" >&2
  exit 1
fi

# Import uses the same IDs as Terraform import.
go run "${ROOT_DIR}/internal/dev/mdexec" -allow-prefix tf_pulumi_ -database "${database}" -sql "CREATE SCHEMA \"${database}\".imported"
pulumi_in sql import motherduck:index/schema:Schema imported "${database}.imported" --yes --protect=false --non-interactive --out "${work_dir}/sql/imported.yaml" > "${work_dir}/sql/import.log" 2>&1
grep -q 'name: imported' "${work_dir}/sql/imported.yaml"
pulumi_in sql refresh --yes --non-interactive > "${work_dir}/sql/refresh-imported.log" 2>&1

pulumi_in sql destroy --yes --non-interactive > "${work_dir}/sql/destroy.log" 2>&1
sql_active=false
remaining="$(mdexec_scalar "SELECT (SELECT count(*) FROM md_list_flights() WHERE flight_name = '${database}') + (SELECT count(*) FROM md_list_dives() WHERE title = '${database}') + (SELECT count(*) FROM md_list_guides(topic := 'tf_pulumi/r${RUN_ID}'))")"
if [[ "${remaining}" != "0" ]]; then
  echo "Expected Pulumi destroy to remove the Flight, Dive, and Guide, ${remaining} remain" >&2
  exit 1
fi
"${ROOT_DIR}/scripts/audit-live-test-cleanup.sh"
echo "PASS: Pulumi SQL create, refresh, no-op, update, replace, import, and destroy"

if [[ -z "${MOTHERDUCK_ADMIN_TOKEN:-}" ]]; then
  echo "Skipping the Pulumi admin lifecycle: MOTHERDUCK_ADMIN_TOKEN is not set"
  exit 0
fi

echo "==> Pulumi admin lifecycle (${RUN_ID})"
admin_active=true
prepare_program admin
configure admin cooldown 60 tokenName initial
converge admin initial
expect_output admin username "${database}"
initial_token="$(output admin tokenId)"

# Renaming the token replaces it. Pulumi creates the new token first.
configure admin cooldown 90 tokenName rotated
converge admin rotated
expect_output admin cooldown 90
expect_output admin tokenName rotated
if [[ "$(output admin tokenId)" == "${initial_token}" ]]; then
  echo "Expected the token rename to issue a new token" >&2
  exit 1
fi

pulumi_in admin destroy --yes --non-interactive > "${work_dir}/admin/destroy.log" 2>&1
admin_active=false
status="$(curl -sS -o /dev/null -w '%{http_code}' -H @- "${MOTHERDUCK_API_BASE_URL:-https://api.motherduck.com}/v1/users/${database}/instances" <<<"Authorization: Bearer ${MOTHERDUCK_ADMIN_TOKEN}")"
if [[ "${status}" != "404" ]]; then
  echo "Expected the service account to be deleted, got HTTP ${status}" >&2
  exit 1
fi
echo "PASS: Pulumi admin create, refresh, no-op, token rotation, and destroy"
