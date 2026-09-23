#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/terraform-test.sh
source "${ROOT_DIR}/scripts/lib/terraform-test.sh"
isolate_live_test_environment
: "${MOTHERDUCK_TOKEN:?SQL token required}"
: "${MOTHERDUCK_ADMIN_TOKEN:?Admin token required}"
RUN_ID="${RUN_ID:-$(date +%Y%m%d%H%M%S)_$$}"
[[ "${RUN_ID}" =~ ^[a-zA-Z0-9_]+$ ]] || exit 1
PROVIDER_VERSION=0.2.10
prepare_provider_mirror
umask 077
result_dir="${ROOT_DIR}/test-results/role-audit-${RUN_ID}"
mkdir -p "${result_dir}/fixture" "${result_dir}/audit"
write_provider_cli_config "${result_dir}/terraformrc"
export TF_CLI_CONFIG_FILE="${result_dir}/terraformrc"
prefix="tf_e06_${RUN_ID}"
cat > "${result_dir}/fixture/main.tf" <<HCL
terraform {
  required_providers {
    motherduck = { source = "motherduckdb/motherduck", version = "= 0.2.10" }
  }
}
provider "motherduck" {}
resource "motherduck_role" "team" { name = "${prefix}_team" }
resource "motherduck_role" "child" { name = "${prefix}_child" }
resource "motherduck_role_grant" "platform" {
 role_name = "builder"
 grantee_name = motherduck_role.team.name
 grantee_type = "role"
}
resource "motherduck_role_grant" "child" {
 role_name = motherduck_role.team.name
 grantee_name = motherduck_role.child.name
 grantee_type = "role"
}
resource "motherduck_service_account" "member" { username = "${prefix}_member" }
resource "motherduck_service_account" "extra" { username = "${prefix}_extra" }
resource "motherduck_role_grant" "member" {
 role_name = motherduck_role.team.name
 grantee_name = motherduck_service_account.member.username
 grantee_type = "user"
}
HCL
cp "${ROOT_DIR}/examples/role-access-audit/"*.tf "${result_dir}/audit/"
cat > "${result_dir}/audit/terraform.tfvars" <<HCL
expected_roles = {
 "${prefix}_team" = {
   members = ["user:${prefix}_member", "role:${prefix}_child"]
   inherits = ["builder"]
 }
}
HCL
cleanup() {
  local status=$?
  trap - EXIT
  if [[ -d "${result_dir}/fixture/.terraform" ]]; then
    terraform -chdir="${result_dir}/fixture" plan -destroy -input=false -out=destroy.tfplan >"${result_dir}/cleanup-plan.log" 2>&1 &&
      terraform -chdir="${result_dir}/fixture" apply -input=false destroy.tfplan >"${result_dir}/cleanup.log" 2>&1 || status=1
  fi
  if [[ "${status}" -ne 0 ]]; then echo "Role audit failed. State/logs retained at ${result_dir}" >&2; fi
  exit "${status}"
}
trap cleanup EXIT
terraform -chdir="${result_dir}/fixture" init -backend=false -input=false >"${result_dir}/init-fixture.log" 2>&1
terraform -chdir="${result_dir}/fixture" plan -input=false -out=create.tfplan >"${result_dir}/plan-fixture.log" 2>&1
terraform -chdir="${result_dir}/fixture" apply -input=false create.tfplan >"${result_dir}/apply-fixture.log" 2>&1
terraform -chdir="${result_dir}/audit" init -backend=false -input=false >"${result_dir}/init-audit.log" 2>&1
terraform -chdir="${result_dir}/audit" plan -input=false -var=fail_on_drift=true -out=audit.tfplan >"${result_dir}/plan-audit.log" 2>&1
terraform -chdir="${result_dir}/audit" apply -input=false audit.tfplan >"${result_dir}/apply-audit.log" 2>&1
terraform -chdir="${result_dir}/audit" output -json >"${result_dir}/audit.json"
jq -e '.compliant.value == true and ([.audit.value[].inherited_roles[]] | index("explorer") != null)' "${result_dir}/audit.json" >/dev/null
terraform -chdir="${result_dir}/audit" plan -input=false -var=fail_on_drift=true -detailed-exitcode >"${result_dir}/noop.log" 2>&1
go run "${ROOT_DIR}/internal/dev/mdexec" -sql "GRANT ROLE \"${prefix}_team\" TO USER \"${prefix}_extra\"" >"${result_dir}/extra-grant.log" 2>&1
terraform -chdir="${result_dir}/audit" apply -auto-approve -input=false >"${result_dir}/drift-report.log" 2>&1
terraform -chdir="${result_dir}/audit" output -json >"${result_dir}/drift.json"
jq -e --arg principal "user:${prefix}_extra" '.compliant.value == false and ([.audit.value[].unexpected_members[]] | index($principal) != null)' "${result_dir}/drift.json" >/dev/null
for attempt in 1 2; do
  if terraform -chdir="${result_dir}/audit" plan -input=false -var=fail_on_drift=true >"${result_dir}/rejected-${attempt}.log" 2>&1; then exit 1; fi
  grep -q 'Role access drift detected' "${result_dir}/rejected-${attempt}.log"
done
go run "${ROOT_DIR}/internal/dev/mdexec" -sql "REVOKE ROLE \"${prefix}_team\" FROM USER \"${prefix}_extra\"; REVOKE ROLE \"${prefix}_team\" FROM USER \"${prefix}_member\"" >"${result_dir}/missing-grant.log" 2>&1
terraform -chdir="${result_dir}/audit" apply -auto-approve -input=false >"${result_dir}/missing-report.log" 2>&1
terraform -chdir="${result_dir}/audit" output -json >"${result_dir}/missing.json"
jq -e --arg principal "user:${prefix}_member" '[.audit.value[].missing_members[]] | index($principal) != null' "${result_dir}/missing.json" >/dev/null
terraform -chdir="${result_dir}/fixture" plan -input=false -out=restore.tfplan >"${result_dir}/restore-plan.log" 2>&1
terraform -chdir="${result_dir}/fixture" apply -input=false restore.tfplan >"${result_dir}/restore.log" 2>&1
terraform -chdir="${result_dir}/audit" apply -auto-approve -input=false -var=fail_on_drift=true >"${result_dir}/restored-audit.log" 2>&1
terraform -chdir="${result_dir}/audit" destroy -auto-approve -input=false >"${result_dir}/audit-destroy.log" 2>&1
printf 'PASS: direct/inherited roles, extra/missing user, persistent drift, restoration. Fixture teardown follows.\n'
