#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TERRAFORM_BIN="${TERRAFORM_BIN:-terraform}"
PROVIDER_VERSION="0.2.13"
RUN_DIR="${TMPDIR:-/tmp}/motherduck-example-lifecycle-$$"
MIRROR_DIR="${RUN_DIR}/provider-mirror"
CLI_CONFIG="${RUN_DIR}/terraformrc"

cleanup() {
  if [[ -n "${REST_SERVER_PID:-}" ]]; then
    kill "${REST_SERVER_PID}" 2>/dev/null || true
    wait "${REST_SERVER_PID}" 2>/dev/null || true
  fi
  rm -rf "${RUN_DIR}"
}
trap cleanup EXIT

mkdir -p "${MIRROR_DIR}" "${RUN_DIR}/provider-bin"

provider_binary="${TF_TEST_PROVIDER_BINARY:-${RUN_DIR}/provider-bin/terraform-provider-motherduck_v${PROVIDER_VERSION}}"
if [[ -z "${TF_TEST_PROVIDER_BINARY:-}" ]]; then
  go build -o "${provider_binary}" "${ROOT_DIR}"
fi

for host in registry.terraform.io registry.opentofu.org; do
  target="${MIRROR_DIR}/${host}/motherduckdb/motherduck/${PROVIDER_VERSION}/$(go env GOOS)_$(go env GOARCH)"
  mkdir -p "${target}"
  ln -s "${provider_binary}" "${target}/terraform-provider-motherduck_v${PROVIDER_VERSION}"
done

cat >"${CLI_CONFIG}" <<HCL
provider_installation {
  filesystem_mirror {
    path    = "${MIRROR_DIR}"
    include = ["registry.terraform.io/motherduckdb/motherduck", "registry.opentofu.org/motherduckdb/motherduck"]
  }
  direct {
    exclude = ["registry.terraform.io/motherduckdb/motherduck", "registry.opentofu.org/motherduckdb/motherduck"]
  }
}
HCL

write_module_root() {
  local name="$1"
  local source="$2"
  local body="$3"
  local dir="${RUN_DIR}/${name}"
  mkdir -p "${dir}"
  cat >"${dir}/main.tf" <<HCL
terraform {
  required_version = ">= 1.5.0"
  required_providers {
    motherduck = {
      source  = "motherduckdb/motherduck"
      version = "= ${PROVIDER_VERSION}"
    }
  }
}

provider "motherduck" {}

module "example" {
  source = "${source}"
${body}
}

output "share_urls" {
  value     = try(module.example.share_urls, {})
  sensitive = true
}

output "reader_setup_tokens" {
  value     = try(module.example.reader_setup_tokens, {})
  sensitive = true
}

output "reader_rotation_tokens" {
  value     = try(module.example.reader_rotation_tokens, {})
  sensitive = true
}

output "writer_rotation_tokens" {
  value     = try(module.example.writer_rotation_tokens, {})
  sensitive = true
}

output "writer_token" {
  value     = try(module.example.writer_token, null)
  sensitive = true
}
HCL
  TF_CLI_CONFIG_FILE="${CLI_CONFIG}" "${TERRAFORM_BIN}" -chdir="${dir}" init -backend=false -input=false >/dev/null
  TF_CLI_CONFIG_FILE="${CLI_CONFIG}" "${TERRAFORM_BIN}" -chdir="${dir}" validate >/dev/null
  printf '%s\n' "${dir}"
}

plan_module() {
  local dir="$1"
  local plan="${dir}/example.tfplan"
  MOTHERDUCK_TOKEN="offline-sql-token" MOTHERDUCK_ADMIN_TOKEN="offline-admin-token" \
    TF_CLI_CONFIG_FILE="${CLI_CONFIG}" "${TERRAFORM_BIN}" -chdir="${dir}" \
    plan -refresh=false -input=false -no-color -out="${plan}" >/dev/null || return
  TF_CLI_CONFIG_FILE="${CLI_CONFIG}" "${TERRAFORM_BIN}" -chdir="${dir}" show -json "${plan}"
}

hyper_dir="$(write_module_root hypertenancy "${ROOT_DIR}/examples/blueprints/hypertenancy" '  tenants = {
    acme = { display_name = "Acme" }
  }
  reader_token_generations   = ["rotation_2026_10"]
  retire_legacy_reader_token = false')"
hyper_plan="$(plan_module "${hyper_dir}")"
jq -e '([.resource_changes[].address] | any(. == "module.example.motherduck_access_token.reader_setup[\"acme\"]")) and ([.resource_changes[].address] | any(. == "module.example.motherduck_access_token.reader_rotation[\"acme/rotation_2026_10\"]")) and (.planned_values.outputs.share_urls.sensitive == true) and (.planned_values.outputs.reader_setup_tokens.sensitive == true) and (.planned_values.outputs.reader_rotation_tokens.sensitive == true)' <<<"${hyper_plan}" >/dev/null

read_dir="$(write_module_root read_hypertenancy "${ROOT_DIR}/examples/blueprints/read-hypertenancy" '  tenants = {
    acme = { display_name = "Acme" }
  }
  reader_token_generations   = ["rotation_2026_10"]
  retire_legacy_reader_token = false')"
read_plan="$(plan_module "${read_dir}")"
jq -e '([.resource_changes[].address] | any(. == "module.example.motherduck_access_token.reader_setup[\"acme\"]")) and ([.resource_changes[].address] | any(. == "module.example.motherduck_access_token.reader_rotation[\"acme/rotation_2026_10\"]"))' <<<"${read_plan}" >/dev/null

writer_dir="$(write_module_root writer_bootstrap "${ROOT_DIR}/examples/blueprints/writer-bootstrap" '  writer_username         = "svc_writer_example"
  writer_token_generations = ["rotation_2026_10"]')"
writer_plan="$(plan_module "${writer_dir}")"
jq -e '([.resource_changes[].address] | any(. == "module.example.motherduck_access_token.writer_legacy[0]")) and ([.resource_changes[].address] | any(. == "module.example.motherduck_access_token.writer_rotation[\"rotation_2026_10\"]")) and (.planned_values.outputs.writer_rotation_tokens.sensitive == true)' <<<"${writer_plan}" >/dev/null
writer_retire_dir="$(write_module_root writer_bootstrap_retire "${ROOT_DIR}/examples/blueprints/writer-bootstrap" '  writer_username         = "svc_writer_example"
  writer_token_generations = ["rotation_2026_10"]
  retire_legacy_writer_token = true')"

# The service must show a replacement token from a prior apply. A fresh
# generation and retirement in one plan must fail without contacting MotherDuck.
REST_PORT_FILE="${RUN_DIR}/rest-port"
REST_EXISTING_TOKEN_FILE="${RUN_DIR}/rest-existing-tokens"
python3 -u - "${REST_PORT_FILE}" "${REST_EXISTING_TOKEN_FILE}" <<'PYTHON' >"${RUN_DIR}/rest-server.log" 2>&1 &
import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if not self.path.startswith("/v1/users/") or not self.path.endswith("/tokens"):
            self.send_error(404)
            return
        tokens = []
        if Path(sys.argv[2]).exists():
            username = self.path.split("/")[3]
            if username == "svc_writer_example":
                tokens = [{"id": "writer-rotation", "name": "terraform-writer-rotation_2026_10", "token_type": "read_write"}]
            else:
                tokens = [{"id": "reader-rotation", "name": "terraform-reader-rotation_2026_10", "token_type": "read_scaling"}]
        body = json.dumps({"tokens": tokens}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_):
        pass


server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
Path(sys.argv[1]).write_text(str(server.server_address[1]))
server.serve_forever()
PYTHON
REST_SERVER_PID=$!
for _ in {1..50}; do
  [[ -s "${REST_PORT_FILE}" ]] && break
  sleep 0.1
done
[[ -s "${REST_PORT_FILE}" ]]
REST_URL="http://127.0.0.1:$(<"${REST_PORT_FILE}")"
assert_retirement_rejected() {
  local dir="$1"
  if MOTHERDUCK_API_BASE_URL="${REST_URL}" plan_module "${dir}" >"${dir}/rejected-plan.log" 2>&1; then
    echo "Same-apply token retirement unexpectedly planned successfully in ${dir}" >&2
    exit 1
  fi
  grep -Eq 'Create and verify a (writer|reader) rotation token' "${dir}/rejected-plan.log"
}
assert_retirement_rejected "${writer_retire_dir}"

for example in hypertenancy read-hypertenancy; do
  retire_dir="$(write_module_root "${example}_retire" "${ROOT_DIR}/examples/blueprints/${example}" '  tenants = {
    acme = { display_name = "Acme" }
  }
  reader_token_generations = ["rotation_2026_10"]
  retire_legacy_reader_token = true')"
  assert_retirement_rejected "${retire_dir}"
done
touch "${REST_EXISTING_TOKEN_FILE}"
for dir in "${writer_retire_dir}" "${RUN_DIR}/hypertenancy_retire" "${RUN_DIR}/read-hypertenancy_retire"; do
  accepted_plan="$(MOTHERDUCK_API_BASE_URL="${REST_URL}" plan_module "${dir}")"
  jq -e 'any(.checks[]; .address.type == "motherduck_service_account" and .status == "pass")' <<<"${accepted_plan}" >/dev/null
done

cfa_dir="${RUN_DIR}/customer-facing-analytics"
mkdir -p "${cfa_dir}"
cp "${ROOT_DIR}/examples/customer-facing-analytics"/*.tf "${cfa_dir}/"
TF_CLI_CONFIG_FILE="${CLI_CONFIG}" "${TERRAFORM_BIN}" -chdir="${cfa_dir}" init -backend=false -input=false >/dev/null
TF_CLI_CONFIG_FILE="${CLI_CONFIG}" "${TERRAFORM_BIN}" validate >/dev/null
MOTHERDUCK_TOKEN="offline-sql-token" MOTHERDUCK_ADMIN_TOKEN="offline-admin-token" \
  TF_CLI_CONFIG_FILE="${CLI_CONFIG}" "${TERRAFORM_BIN}" -chdir="${cfa_dir}" \
  plan -refresh=false -input=false -no-color -var='suspended_tenants=["acme"]' -out="${cfa_dir}/example.tfplan" >/dev/null
cfa_plan="$(TF_CLI_CONFIG_FILE="${CLI_CONFIG}" "${TERRAFORM_BIN}" -chdir="${cfa_dir}" show -json "${cfa_dir}/example.tfplan")"
jq -e '([.resource_changes[] | select(.address == "motherduck_database.tenant[\"acme\"]") | .change.actions] == [["create"]]) and ([.resource_changes[] | select(.address == "motherduck_table.daily_usage[\"acme\"]") | .change.actions] == [["create"]]) and ([.resource_changes[] | select(.address == "motherduck_service_account.reader[\"acme\"]") | .change.actions] == [["create"]]) and ([.resource_changes[] | select(.address == "motherduck_share_grant.reader[\"acme\"]")] | length == 0) and ([.resource_changes[] | select(.address == "motherduck_access_token.reader[\"acme\"]")] | length == 0) and (.planned_values.outputs.tenants.value.acme.suspended == true) and (.planned_values.outputs.suspended_tenants.value == ["acme"])' <<<"${cfa_plan}" >/dev/null

printf '%s\n' "Example lifecycle offline checks passed"
