#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OS="$(go env GOOS)"
ARCH="$(go env GOARCH)"

PROVIDER_VERSION="${PROVIDER_VERSION:-0.1.0}"
SOURCE_HOST="registry.terraform.io"
SOURCE_NAMESPACE="motherduckdb"
SOURCE_TYPE="motherduck"
PROVIDER_SOURCE="${SOURCE_NAMESPACE}/${SOURCE_TYPE}"
RUN_ID="${RUN_ID:-$(date +%Y%m%d%H%M%S)_$$}"
TERRAFORM_BIN="${TERRAFORM_BIN:-terraform}"

terraform_cli_version() {
  "${TERRAFORM_BIN}" version -json 2>/dev/null | sed -n 's/.*"terraform_version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p'
}

terraform_supports_ephemeral_examples() {
  local version="$1"
  local major
  local minor

  if [[ -z "${version}" ]]; then
    return 1
  fi

  IFS=. read -r major minor _ <<<"${version}"
  if [[ "${major}" =~ ^[0-9]+$ && "${minor}" =~ ^[0-9]+$ ]] && ((major > 1 || (major == 1 && minor >= 10))); then
    return 0
  fi
  return 1
}

TERRAFORM_CLI_VERSION="$(terraform_cli_version)"
if terraform_supports_ephemeral_examples "${TERRAFORM_CLI_VERSION}"; then
  EPHEMERAL_EXAMPLES_SUPPORTED=1
else
  EPHEMERAL_EXAMPLES_SUPPORTED=0
fi

result_dir="${ROOT_DIR}/test-results/examples-${RUN_ID}"
if [[ -e "${result_dir}" ]]; then
  echo "Refusing to reuse existing test directory: ${result_dir}" >&2
  exit 1
fi
mkdir -p "${result_dir}"

provider_dir="${PROVIDER_BIN_DIR:-${ROOT_DIR}/tools/provider-bin/${RUN_ID}}"
mirror_dir="${result_dir}/provider-mirror"
mkdir -p "${provider_dir}" "${mirror_dir}/${SOURCE_HOST}/${SOURCE_NAMESPACE}/${SOURCE_TYPE}/${PROVIDER_VERSION}/${OS}_${ARCH}"

provider_binary="${provider_dir}/terraform-provider-${SOURCE_TYPE}_v${PROVIDER_VERSION}"
GOOS="${OS}" GOARCH="${ARCH}" go build -o "${provider_binary}" "${ROOT_DIR}"
cp "${provider_binary}" "${mirror_dir}/${SOURCE_HOST}/${SOURCE_NAMESPACE}/${SOURCE_TYPE}/${PROVIDER_VERSION}/${OS}_${ARCH}/"

cli_config="${result_dir}/terraformrc"
cat > "${cli_config}" <<HCL
provider_installation {
  filesystem_mirror {
    path    = "${mirror_dir}"
    include = ["${PROVIDER_SOURCE}"]
  }
  direct {
    exclude = ["${PROVIDER_SOURCE}"]
  }
}
HCL

write_plan_vars() {
  local relative_dir="$1"
  local work_dir="$2"

  case "${relative_dir}" in
    examples/blueprints/hypertenancy)
      cat > "${work_dir}/terraform.tfvars" <<'HCL'
database_prefix = "tenant_example"
reader_prefix   = "svc_reader_example"
share_prefix    = "share_example"

tenants = {
  acme = {
    display_name            = "Acme"
    slug                    = "Acme.Primary"
    snapshot_retention_days = 7
  }
  "north-america" = {
    display_name            = "North America"
    snapshot_retention_days = 14
  }
}
HCL
      ;;
    examples/blueprints/writer-bootstrap)
      cat > "${work_dir}/terraform.tfvars" <<'HCL'
writer_username = "svc_writer_example"
HCL
      ;;
    examples/blueprints/read-hypertenancy)
      cat > "${work_dir}/terraform.tfvars" <<'HCL'
database_prefix = "tenant_example"
reader_prefix   = "svc_reader_example"
share_prefix    = "share_example"

tenants = {
  acme = {
    display_name            = "Acme"
    slug                    = "Acme.Primary"
    snapshot_retention_days = 7
  }
  "north-america" = {
    display_name            = "North America"
    snapshot_retention_days = 14
  }
}
HCL
      ;;
    examples/provider)
      cat > "${work_dir}/terraform.tfvars" <<'HCL'
motherduck_token       = "dummy-sql-token"
motherduck_admin_token = "dummy-admin-token"
HCL
      ;;
    examples/resources/motherduck_secret)
      cat > "${work_dir}/terraform.tfvars" <<'HCL'
aws_access_key_id     = "dummy-access-key"
aws_secret_access_key = "dummy-secret-key"
HCL
      ;;
  esac
}

assert_invalid_blueprint_vars() {
  local relative_dir="$1"
  local work_dir="$2"
  local invalid_vars="${work_dir}/invalid-blueprint.tfvars"

  case "${relative_dir}" in
    examples/blueprints/hypertenancy)
      cat > "${invalid_vars}" <<'HCL'
database_prefix = "bad.prefix"
reader_prefix = "1bad"
reader_token_ttl_seconds = 299
tenants = {}
HCL
      ;;
    examples/blueprints/read-hypertenancy)
      cat > "${invalid_vars}" <<'HCL'
database_prefix = "bad.prefix"
reader_token_ttl_seconds = 299
tenants = {}
HCL
      ;;
    examples/blueprints/writer-bootstrap)
      cat > "${invalid_vars}" <<'HCL'
writer_username = "1-bad-writer"
writer_token_ttl_seconds = 299
HCL
      ;;
    *)
      return 0
      ;;
  esac

  set +e
  invalid_output="$(
    MOTHERDUCK_TOKEN="dummy-sql-token" \
      MOTHERDUCK_ADMIN_TOKEN="dummy-admin-token" \
      TF_CLI_CONFIG_FILE="${cli_config}" \
      "${TERRAFORM_BIN}" -chdir="${work_dir}" plan -refresh=false -input=false -no-color -var-file="${invalid_vars}" 2>&1
  )"
  invalid_exit=$?
  set -e
  rm -f "${invalid_vars}"

  if [[ "${invalid_exit}" -eq 0 ]]; then
    echo "Expected invalid blueprint plan to fail for ${relative_dir}" >&2
    exit 1
  fi
  if [[ "${relative_dir}" == examples/blueprints/writer-bootstrap ]]; then
    if [[ "${invalid_output}" != *"writer_username must start with an ASCII letter"* || "${invalid_output}" != *"writer_token_ttl_seconds must be between 300 and 31536000 seconds"* ]]; then
      echo "Expected invalid writer-bootstrap diagnostics for ${relative_dir}, got:" >&2
      printf '%s\n' "${invalid_output}" >&2
      exit 1
    fi
    return 0
  fi
  if [[ "${invalid_output}" != *"database_prefix must start with an ASCII letter"* || "${invalid_output}" != *"reader_token_ttl_seconds must be between 300 and 31536000 seconds"* || "${invalid_output}" != *"tenants must include at least one tenant"* ]]; then
    echo "Expected invalid blueprint diagnostics for ${relative_dir}, got:" >&2
    printf '%s\n' "${invalid_output}" >&2
    exit 1
  fi
}

while IFS= read -r example_dir; do
  relative_dir="${example_dir#"${ROOT_DIR}/"}"
  if [[ "${relative_dir}" == examples/ephemeral-resources/* && "${EPHEMERAL_EXAMPLES_SUPPORTED}" != "1" ]]; then
    echo "==> Skipping ${relative_dir}; Terraform ${TERRAFORM_CLI_VERSION:-unknown} does not support ephemeral blocks"
    continue
  fi

  safe_name="${relative_dir//\//__}"
  work_dir="${result_dir}/${safe_name}"
  mkdir -p "${work_dir}"
  find "${example_dir}" -maxdepth 1 -type f -name '*.tf' -exec cp {} "${work_dir}/" \;

  if ! grep -R 'source[[:space:]]*=[[:space:]]*"motherduckdb/motherduck"' "${work_dir}"/*.tf >/dev/null 2>&1; then
    cat > "${work_dir}/terraform-provider-override.tf" <<HCL
terraform {
  required_version = ">= 1.5.0"

  required_providers {
    motherduck = {
      source  = "motherduckdb/motherduck"
      version = "= ${PROVIDER_VERSION}"
    }
  }
}
HCL
  else
    perl -0pi -e "s/version = \">= 0\\.1\\.0\"/version = \"= ${PROVIDER_VERSION}\"/" "${work_dir}"/*.tf
  fi
  write_plan_vars "${relative_dir}" "${work_dir}"

  echo "==> Validating ${relative_dir}"
  TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" init -backend=false -input=false
  TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" validate

  if [[ "${relative_dir}" == examples/data-sources/* ]]; then
    echo "==> Skipping offline plan for ${relative_dir}; Terraform reads data sources during plan"
    continue
  fi
  if [[ "${relative_dir}" == examples/ephemeral-resources/* ]]; then
    echo "==> Skipping offline plan for ${relative_dir}; Terraform opens ephemeral resources during plan"
    continue
  fi

  echo "==> Planning ${relative_dir}"
  set +e
  plan_output="$(
    MOTHERDUCK_TOKEN="dummy-sql-token" \
      MOTHERDUCK_ADMIN_TOKEN="dummy-admin-token" \
      TF_CLI_CONFIG_FILE="${cli_config}" \
      "${TERRAFORM_BIN}" -chdir="${work_dir}" plan -refresh=false -input=false -no-color -out="${work_dir}/example.tfplan" 2>&1
  )"
  plan_exit=$?
  set -e
  if [[ "${plan_exit}" -ne 0 ]]; then
    echo "Example plan failed for ${relative_dir}:" >&2
    printf '%s\n' "${plan_output}" >&2
    exit 1
  fi

  case "${relative_dir}" in
    examples/blueprints/hypertenancy|examples/blueprints/read-hypertenancy)
      plan_json="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" show -json "${work_dir}/example.tfplan")"
      for expected in \
        "tenant_example_acme_primary" \
        "tenant_example_north_america" \
        "svc_reader_example_acme_primary" \
        "svc_reader_example_north_america" \
        "share_example_acme_primary" \
        "share_example_north_america"; do
        if [[ "${plan_json}" != *"${expected}"* ]]; then
          echo "Expected blueprint plan for ${relative_dir} to include normalized name ${expected}" >&2
          exit 1
        fi
      done
      assert_invalid_blueprint_vars "${relative_dir}" "${work_dir}"
      ;;
    examples/blueprints/writer-bootstrap)
      plan_json="$(TF_CLI_CONFIG_FILE="${cli_config}" "${TERRAFORM_BIN}" -chdir="${work_dir}" show -json "${work_dir}/example.tfplan")"
      if [[ "${plan_json}" != *"svc_writer_example"* ]]; then
        echo "Expected blueprint plan for ${relative_dir} to include writer username svc_writer_example" >&2
        exit 1
      fi
      assert_invalid_blueprint_vars "${relative_dir}" "${work_dir}"
      ;;
  esac
done < <(find "${ROOT_DIR}/examples" -type f -name '*.tf' -exec dirname {} \; | sort -u)
