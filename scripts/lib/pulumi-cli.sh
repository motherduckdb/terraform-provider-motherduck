#!/usr/bin/env bash

# Pulumi CLI and Any Terraform Provider bridge versions used by the Pulumi
# checks. Bump both together with the example in examples/pulumi/python.
PULUMI_CLI_VERSION="3.265.0"
# shellcheck disable=SC2034
PULUMI_BRIDGE_VERSION="1.4.0"

# pulumi_cli_digest <platform> prints the pinned SHA-256 of the release archive
# for that platform, so a replaced upstream archive fails verification even if
# its published checksum file is replaced with it.
pulumi_cli_digest() {
  case "$1" in
    linux-x64) echo "a7eff63bc539e4319a8add7383a01e427aef74a8994bcf5e6a9d12bf9d674979" ;;
    linux-arm64) echo "55f2acb3db3618ec7405cbdf2bb21566fdf46c282a92ef419d9b1d7298a83ee4" ;;
    darwin-x64) echo "ef23180967a62aed4fe86fd64e0a2814d907a112952e93af7e354ed7f9ca3f8a" ;;
    darwin-arm64) echo "659e825627a45e781a290df53f801f60a21e0c206f351d08d68beadbb1fbaafe" ;;
    *)
      echo "No pinned Pulumi CLI digest for platform $1" >&2
      return 1
      ;;
  esac
}

pulumi_cli_platform() {
  local os arch
  case "$(uname -s)" in
    Linux) os="linux" ;;
    Darwin) os="darwin" ;;
    *)
      echo "Unsupported operating system for the Pulumi CLI: $(uname -s)" >&2
      return 1
      ;;
  esac
  case "$(uname -m)" in
    x86_64 | amd64) arch="x64" ;;
    arm64 | aarch64) arch="arm64" ;;
    *)
      echo "Unsupported architecture for the Pulumi CLI: $(uname -m)" >&2
      return 1
      ;;
  esac
  echo "${os}-${arch}"
}

# install_pulumi_cli <directory> downloads the pinned Pulumi CLI into
# <directory>/pulumi, verifies it against the release checksum file and the
# pinned digest, and prints the path of the pulumi binary.
install_pulumi_cli() {
  local install_dir="$1"
  local platform expected archive_name archive_path actual
  platform="$(pulumi_cli_platform)" || return 1
  expected="$(pulumi_cli_digest "${platform}")" || return 1
  archive_name="pulumi-v${PULUMI_CLI_VERSION}-${platform}.tar.gz"
  archive_path="${install_dir}/${archive_name}"
  local release_url="https://github.com/pulumi/pulumi/releases/download/v${PULUMI_CLI_VERSION}"
  if [[ -x "${install_dir}/pulumi/pulumi" ]]; then
    echo "${install_dir}/pulumi/pulumi"
    return
  fi
  download_verified_archive \
    "${release_url}/${archive_name}" \
    "${release_url}/pulumi-${PULUMI_CLI_VERSION}-checksums.txt" \
    "${archive_path}" >&2 || return 1
  actual="$(sha256_file "${archive_path}")" || return 1
  if [[ "${actual}" != "${expected}" ]]; then
    echo "Pulumi CLI archive ${archive_name} has digest ${actual}, expected the pinned ${expected}" >&2
    rm -f "${archive_path}"
    return 1
  fi
  tar -xzf "${archive_path}" -C "${install_dir}" || return 1
  rm -f "${archive_path}"
  echo "${install_dir}/pulumi/pulumi"
}

# pulumi_isolated_env <work_dir> exports a Pulumi home and file backend inside
# <work_dir>, so checks never read or write the caller's Pulumi login, plugins,
# or stacks.
pulumi_isolated_env() {
  local work_dir="$1"
  mkdir -p "${work_dir}/pulumi-home" "${work_dir}/backend"
  export PULUMI_HOME="${work_dir}/pulumi-home"
  export PULUMI_BACKEND_URL="file://${work_dir}/backend"
  export PULUMI_SKIP_UPDATE_CHECK=true
  export PULUMI_DIY_BACKEND_NO_LEGACY_WARNING=true
  if [[ -z "${PULUMI_CONFIG_PASSPHRASE:-}" ]]; then
    PULUMI_CONFIG_PASSPHRASE="$(openssl rand -hex 32)"
    export PULUMI_CONFIG_PASSPHRASE
  fi
}

# pulumi_use_local_provider <Pulumi.yaml> points a copied program at
# ./bin/terraform-provider-motherduck, so checks exercise the provider built
# from this checkout instead of the released version the example pins.
pulumi_use_local_provider() {
  local manifest="$1"
  local registry_parameters='      - registry.terraform.io/motherduckdb/motherduck\n      - [0-9]+\.[0-9]+\.[0-9]+\n'
  if ! grep -q 'registry.terraform.io/motherduckdb/motherduck' "${manifest}"; then
    echo "${manifest} does not declare the MotherDuck registry provider" >&2
    return 1
  fi
  perl -0pi -e "s|${registry_parameters}|      - ./bin/terraform-provider-motherduck\\n|" "${manifest}"
  if grep -q 'registry.terraform.io/motherduckdb/motherduck' "${manifest}"; then
    echo "Failed to point ${manifest} at the local provider" >&2
    return 1
  fi
}

# resolve_pulumi_bin <work_dir> prints PULUMI_BIN when set, or installs the
# pinned CLI under <work_dir>/tools.
resolve_pulumi_bin() {
  local work_dir="$1"
  if [[ -n "${PULUMI_BIN:-}" ]]; then
    echo "${PULUMI_BIN}"
    return
  fi
  mkdir -p "${work_dir}/tools"
  install_pulumi_cli "${work_dir}/tools"
}
