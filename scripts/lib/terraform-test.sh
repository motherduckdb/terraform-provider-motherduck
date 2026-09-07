#!/usr/bin/env bash

# CLI tests share installation mechanics, not resource-specific assertions.
# Reuse is explicit and limited to a suite's freshly built provider binary.
prepare_provider_mirror() {
  umask 077
  PROVIDER_VERSION="${PROVIDER_VERSION:-0.1.0}"
  RUN_ID="${RUN_ID:-$(date +%Y%m%d%H%M%S)_$$}"
  OS="$(go env GOOS)"
  ARCH="$(go env GOARCH)"
  PROVIDER_SOURCE="motherduckdb/motherduck"

  if [[ ! "${PROVIDER_VERSION}" =~ ^[0-9]+[.][0-9]+[.][0-9]+$ ]]; then
    echo "PROVIDER_VERSION must be a numeric major.minor.patch version" >&2
    return 1
  fi
  if [[ ! "${RUN_ID}" =~ ^[A-Za-z0-9_-]+$ ]]; then
    echo "RUN_ID must contain only letters, numbers, underscores, or hyphens" >&2
    return 1
  fi

  if [[ -n "${TF_TEST_PROVIDER_BINARY:-}" ]]; then
    provider_binary="${TF_TEST_PROVIDER_BINARY}"
    if [[ "${provider_binary}" != /* || ! -x "${provider_binary}" ]]; then
      echo "TF_TEST_PROVIDER_BINARY must be an absolute path to an executable" >&2
      return 1
    fi
  else
    provider_dir="${PROVIDER_BIN_DIR:-${ROOT_DIR}/tools/provider-bin/${RUN_ID}}"
    mkdir -p "${provider_dir}" || return
    provider_dir="$(cd "${provider_dir}" && pwd)" || return
    provider_binary="${provider_dir}/terraform-provider-motherduck_v${PROVIDER_VERSION}"
    if [[ -e "${provider_binary}" ]]; then
      echo "Refusing to overwrite an existing test provider: ${provider_binary}" >&2
      return 1
    fi
    go build -o "${provider_binary}" "${ROOT_DIR}" || return
  fi

  mirror_dir="${mirror_dir:-${PROVIDER_MIRROR_DIR:-${ROOT_DIR}/tools/provider-mirror/${RUN_ID}}}"
  mkdir -p "${mirror_dir}" || return
  mirror_dir="$(cd "${mirror_dir}" && pwd)" || return
  local host package_dir target
  for host in registry.terraform.io registry.opentofu.org; do
    package_dir="${mirror_dir}/${host}/${PROVIDER_SOURCE}/${PROVIDER_VERSION}/${OS}_${ARCH}"
    mkdir -p "${package_dir}" || return
    target="${package_dir}/terraform-provider-motherduck_v${PROVIDER_VERSION}"
    if [[ -L "${target}" && "$(readlink "${target}")" == "${provider_binary}" ]]; then
      continue
    fi
    # Do not overwrite another run's binary or mirror. Both CLIs use this one
    # artifact without making a full binary copy for every fixture.
    ln -s "${provider_binary}" "${target}" || return
  done
}

write_provider_cli_config() {
  local config_path="$1"
  local escaped_path="${mirror_dir//\\/\\\\}"
  escaped_path="${escaped_path//\"/\\\"}"
  cat > "${config_path}" <<HCL
provider_installation {
  filesystem_mirror {
    path = "${escaped_path}"
    include = ["registry.terraform.io/motherduckdb/motherduck", "registry.opentofu.org/motherduckdb/motherduck"]
  }
  direct {
    exclude = ["registry.terraform.io/motherduckdb/motherduck", "registry.opentofu.org/motherduckdb/motherduck"]
  }
}
HCL
}

isolate_live_test_environment() {
  # Keep injected service credentials, but never reuse a developer's workspace,
  # state directory, provider process, or implicit CLI/variable overrides.
  unset TF_DATA_DIR TF_WORKSPACE TF_REATTACH_PROVIDERS TF_PLUGIN_CACHE_DIR TF_CLI_CONFIG_FILE
  unset TF_LOG TF_LOG_PATH TF_LOG_PROVIDER TF_LOG_CORE
  local name
  for name in ${!TF_CLI_ARGS@} ${!TF_VAR_@}; do
    unset "${name}"
  done
  export TF_IN_AUTOMATION=1 CHECKPOINT_DISABLE=1
}

isolate_offline_test_environment() {
  # A developer's shell must not inject credentials, CLI flags, variables,
  # another provider process, or a shared state/cache directory into a test.
  unset MOTHERDUCK_TOKEN MOTHERDUCK_ADMIN_TOKEN MOTHERDUCK_API_BASE_URL TF_REATTACH_PROVIDERS
  unset TF_DATA_DIR TF_WORKSPACE TF_PLUGIN_CACHE_DIR
  local name
  for name in ${!TF_CLI_ARGS@} ${!TF_VAR_@}; do
    unset "${name}"
  done
  export TF_IN_AUTOMATION=1 CHECKPOINT_DISABLE=1
}
