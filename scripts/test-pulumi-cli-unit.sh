#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${ROOT_DIR}/scripts/lib/download-checksum.sh"
source "${ROOT_DIR}/scripts/lib/pulumi-cli.sh"

test_dir="$(mktemp -d)"
trap 'rm -rf "${test_dir}"' EXIT

for platform in linux-x64 linux-arm64 darwin-x64 darwin-arm64; do
  if [[ ! "$(pulumi_cli_digest "${platform}")" =~ ^[0-9a-f]{64}$ ]]; then
    echo "Expected a pinned SHA-256 for ${platform}" >&2
    exit 1
  fi
done
if pulumi_cli_digest windows-x64 2>/dev/null; then
  echo "Expected an unpinned platform to fail" >&2
  exit 1
fi

# Every committed Pulumi program installs the released provider, and the
# checks swap in the local build.
for manifest in "${ROOT_DIR}/examples/pulumi/python/Pulumi.yaml" "${ROOT_DIR}"/test-fixtures/pulumi-live/*/Pulumi.yaml; do
  copy="${test_dir}/$(basename "$(dirname "${manifest}")").yaml"
  cp "${manifest}" "${copy}"
  if ! grep -q "version: ${PULUMI_BRIDGE_VERSION}$" "${copy}"; then
    echo "${manifest} must pin bridge ${PULUMI_BRIDGE_VERSION}" >&2
    exit 1
  fi
  pulumi_use_local_provider "${copy}"
  if ! grep -q -- '- ./bin/terraform-provider-motherduck$' "${copy}" || grep -q -E -- '- [0-9]+\.[0-9]+\.[0-9]+$' "${copy}"; then
    echo "Expected ${manifest} to point at only the local provider" >&2
    cat "${copy}" >&2
    exit 1
  fi
done

printf 'name: no-provider\nruntime: yaml\n' > "${test_dir}/none.yaml"
if pulumi_use_local_provider "${test_dir}/none.yaml" 2>/dev/null; then
  echo "Expected a program without the registry provider to be rejected" >&2
  exit 1
fi

if [[ "$(PULUMI_BIN=/opt/pulumi resolve_pulumi_bin "${test_dir}")" != "/opt/pulumi" ]]; then
  echo "Expected PULUMI_BIN to take precedence over the pinned download" >&2
  exit 1
fi

echo "Pulumi CLI helper checks passed"
