#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/terraform-test.sh
source "${ROOT_DIR}/scripts/lib/terraform-test.sh"
# shellcheck source=scripts/lib/live-common.sh
source "${ROOT_DIR}/scripts/lib/live-common.sh"
isolate_live_test_environment

PROVIDER_VERSION="${PROVIDER_VERSION:-0.1.1}"
RUN_ID="${RUN_ID:-$(date +%Y%m%d%H%M%S)_$$}"
TERRAFORM_BIN="${TERRAFORM_BIN:-terraform}"
KEEP_LIVE_FIXTURE="${KEEP_LIVE_FIXTURE:-0}"
require_safe_run_id "${RUN_ID}"

if [[ -z "${MOTHERDUCK_TOKEN:-}" || -z "${MOTHERDUCK_ADMIN_TOKEN:-}" ]]; then
  echo "MOTHERDUCK_TOKEN and MOTHERDUCK_ADMIN_TOKEN are required" >&2
  exit 1
fi

# The role-grant example must own the service-account prerequisite it references.
if rg -q 'grantee_name = "svc_analytics_reader"' "${ROOT_DIR}/examples/resources/motherduck_role_grant/resource.tf" ||
   ! rg -q 'motherduck_service_account\.analytics_reader\.username' "${ROOT_DIR}/examples/resources/motherduck_role_grant/resource.tf"; then
  echo "role-grant example has an unmanaged service-account principal" >&2
  exit 1
fi

prepare_provider_mirror
suffix="${RUN_ID//-/_}"
work_dir="${ROOT_DIR}/test-results/live-role-examples-${RUN_ID}"
if [[ -e "${work_dir}" ]]; then
  echo "Refusing to reuse existing test directory: ${work_dir}" >&2
  exit 1
fi
umask 077
mkdir -p "${work_dir}"
cp "${ROOT_DIR}/examples/resources/motherduck_role/resource.tf" "${work_dir}/role.tf"
cp "${ROOT_DIR}/examples/resources/motherduck_role_grant/resource.tf" "${work_dir}/role-grant.tf"
cp "${ROOT_DIR}/examples/data-sources/motherduck_roles/data-source.tf" "${work_dir}/roles.tf"
cp "${ROOT_DIR}/examples/data-sources/motherduck_role_members/data-source.tf" "${work_dir}/role-members.tf"
cp "${ROOT_DIR}/examples/data-sources/motherduck_roles_for_role/data-source.tf" "${work_dir}/roles-for-role.tf"
cp "${ROOT_DIR}/examples/data-sources/motherduck_roles_for_user/data-source.tf" "${work_dir}/roles-for-user.tf"
perl -0pi -e 's/resource "motherduck_role" "[^"]+" \{\n.*?\n\}\n\n//s' "${work_dir}/role-grant.tf"
perl -0pi -e "s/svc_analytics_reader/svc_role_example_${suffix}/g; s/analytics_readers/role_example_${suffix}/g; s/analytics_reader/role_example_reader/g" "${work_dir}/role.tf" "${work_dir}/role-grant.tf"
perl -0pi -e "s/analytics_readers/role_example_${suffix}/g; s/username = \"svc_analytics_reader\"/username = motherduck_service_account.role_example_reader.username/" "${work_dir}/roles.tf" "${work_dir}/role-members.tf" "${work_dir}/roles-for-role.tf" "${work_dir}/roles-for-user.tf"
perl -0pi -e "s/role_name = \"role_example_${suffix}\"/role_name = motherduck_role.role_example_${suffix}.name/" "${work_dir}/role-members.tf" "${work_dir}/roles-for-role.tf"
cat > "${work_dir}/main.tf" <<HCL
terraform {
  required_providers {
    motherduck = {
      source  = "motherduckdb/motherduck"
      version = "${PROVIDER_VERSION}"
    }
  }
}

provider "motherduck" {}

resource "motherduck_role_grant" "builder_to_reader" {
  role_name    = "builder"
  grantee_name = motherduck_service_account.role_example_reader.username
  grantee_type = "user"
}

resource "motherduck_role" "existing_custom" {
  name = "role_existing_${suffix}"
}

resource "motherduck_role_grant" "existing_to_new" {
  role_name    = motherduck_role.existing_custom.name
  grantee_name = motherduck_role.role_example_${suffix}.name
  grantee_type = "role"
}

HCL
cat "${work_dir}/role.tf" "${work_dir}/role-grant.tf" "${work_dir}/roles.tf" "${work_dir}/role-members.tf" "${work_dir}/roles-for-role.tf" "${work_dir}/roles-for-user.tf" >> "${work_dir}/main.tf"
rm "${work_dir}/role.tf" "${work_dir}/role-grant.tf" "${work_dir}/roles.tf" "${work_dir}/role-members.tf" "${work_dir}/roles-for-role.tf" "${work_dir}/roles-for-user.tf"

cli_config="${work_dir}/terraformrc"
write_provider_cli_config "${cli_config}"
run_tf() { TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" "$@"; }

account_status() {
  MOTHERDUCK_ADMIN_TOKEN="${MOTHERDUCK_ADMIN_TOKEN}" MOTHERDUCK_API_BASE_URL="${MOTHERDUCK_API_BASE_URL:-https://api.motherduck.com}" ROLE_EXAMPLE_USERNAME="svc_role_example_${suffix}" python3 - <<'PY'
import os
import urllib.error
import urllib.parse
import urllib.request

base = os.environ["MOTHERDUCK_API_BASE_URL"].rstrip("/")
url = base + "/v1/users/" + urllib.parse.quote(os.environ["ROLE_EXAMPLE_USERNAME"], safe="") + "/instances"
request = urllib.request.Request(url, headers={"Authorization": "Bearer " + os.environ["MOTHERDUCK_ADMIN_TOKEN"]})
try:
    with urllib.request.urlopen(request, timeout=30) as response:
        print(response.status)
except urllib.error.HTTPError as error:
    print(error.code)
PY
}

cleanup() {
  local rc=0
  if [[ "${KEEP_LIVE_FIXTURE}" != "1" && -d "${work_dir}/.terraform" ]]; then
    run_tf destroy -auto-approve -input=false >/dev/null || rc=$?
  fi
  if [[ "${KEEP_LIVE_FIXTURE}" != "1" && "${rc}" -eq 0 ]]; then
    local http_code
    http_code="$(account_status)" || rc=$?
    if [[ "${http_code:-}" != "404" ]]; then
      echo "expected role example service account cleanup to return 404, got ${http_code:-unknown}" >&2
      rc=1
    fi
  fi
  return "${rc}"
}
trap live_cleanup_on_exit EXIT

echo "==> Live role examples (${RUN_ID})"
run_tf init -backend=false -input=false >/dev/null
run_tf validate
run_tf apply -auto-approve -input=false >/dev/null
if [[ "$(account_status)" != "200" ]]; then
  echo "expected created role example account to return HTTP 200" >&2
  exit 1
fi
set +e
run_tf plan -detailed-exitcode -input=false >/dev/null
plan_rc=$?
set -e
if [[ "${plan_rc}" -ne 0 ]]; then
  echo "expected no-op plan after role example apply, got ${plan_rc}" >&2
  exit "${plan_rc}"
fi

run_tf state rm motherduck_role_grant.service_account >/dev/null
run_tf import motherduck_role_grant.service_account "role_example_${suffix}/user/svc_role_example_${suffix}" >/dev/null
set +e
run_tf plan -detailed-exitcode -input=false >/dev/null
import_rc=$?
set -e
if [[ "${import_rc}" -ne 0 ]]; then
  echo "expected no-op plan after role grant import, got ${import_rc}" >&2
  exit "${import_rc}"
fi

go run "${ROOT_DIR}/internal/dev/mdexec" -sql "REVOKE ROLE \"builder\" FROM USER \"svc_role_example_${suffix}\"" >/dev/null
set +e
run_tf plan -detailed-exitcode -input=false >/dev/null
drift_rc=$?
set -e
if [[ "${drift_rc}" -ne 2 ]]; then
  echo "expected revoked builder grant drift, got ${drift_rc}" >&2
  exit 1
fi
run_tf apply -auto-approve -input=false >/dev/null
set +e
run_tf plan -detailed-exitcode -input=false >/dev/null
repair_rc=$?
set -e
if [[ "${repair_rc}" -ne 0 ]]; then
  echo "expected no-op plan after repairing revoked grant, got ${repair_rc}" >&2
  exit "${repair_rc}"
fi

if [[ "${KEEP_LIVE_FIXTURE}" == "1" ]]; then
  trap - EXIT
  echo "Kept live fixture at ${work_dir}"
  exit 42
fi
