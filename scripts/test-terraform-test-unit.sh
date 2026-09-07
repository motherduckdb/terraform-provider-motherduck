#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/terraform-test.sh
source "${REPO_ROOT}/scripts/lib/terraform-test.sh"
fixture="$(mktemp -d)"
trap 'rm -rf "${fixture}"' EXIT
mkdir -p "${fixture}/bin" "${fixture}/repo with spaces"
export BUILD_LOG="${fixture}/builds"
cat > "${fixture}/bin/go" <<'STUB'
#!/usr/bin/env bash
set -eu
case "$1 $2" in
  'env GOOS') echo darwin ;;
  'env GOARCH') echo arm64 ;;
  'build -o')
    echo build >> "${BUILD_LOG}"
    if [[ "${FAIL_BUILD:-0}" == 1 ]]; then exit 9; fi
    printf '#!/bin/sh\nexit 0\n' > "$3"
    chmod +x "$3"
    ;;
  *) exit 99 ;;
esac
STUB
chmod +x "${fixture}/bin/go"
export PATH="${fixture}/bin:${PATH}"
ROOT_DIR="${fixture}/repo with spaces"
RUN_ID=first
unset TF_TEST_PROVIDER_BINARY PROVIDER_BIN_DIR PROVIDER_MIRROR_DIR
prepare_provider_mirror
for host in registry.terraform.io registry.opentofu.org; do
  target="${mirror_dir}/${host}/motherduckdb/motherduck/0.1.0/darwin_arm64/terraform-provider-motherduck_v0.1.0"
  [[ -x "${target}" && "$(readlink "${target}")" == "${provider_binary}" ]]
done
write_provider_cli_config "${fixture}/terraformrc"
[[ "$(grep -c 'registry.opentofu.org/motherduckdb/motherduck' "${fixture}/terraformrc")" == 2 ]]

# Explicit suite reuse must not trigger another compile; standalone reuse must
# refuse an old binary rather than test stale code or overwrite a running one.
export TF_TEST_PROVIDER_BINARY="${provider_binary}"
prepare_provider_mirror
[[ "$(wc -l < "${BUILD_LOG}" | tr -d ' ')" == 1 ]]
unset TF_TEST_PROVIDER_BINARY
if prepare_provider_mirror > /dev/null 2>&1; then
  echo 'Existing standalone provider was silently reused' >&2; exit 1
fi
RUN_ID=failed
unset mirror_dir
export FAIL_BUILD=1
if prepare_provider_mirror > /dev/null 2>&1; then
  echo 'Build failure was suppressed' >&2; exit 1
fi
unset FAIL_BUILD
[[ ! -e "${ROOT_DIR}/tools/provider-mirror/failed" ]]
TF_TEST_PROVIDER_BINARY="${fixture}/missing"
if prepare_provider_mirror > /dev/null 2>&1; then
  echo 'Missing suite binary was accepted' >&2; exit 1
fi

export MOTHERDUCK_TOKEN=test-token MOTHERDUCK_ADMIN_TOKEN=test-admin MOTHERDUCK_API_BASE_URL=https://unexpected.invalid
export TF_CLI_ARGS=-destroy TF_CLI_ARGS_plan=-destroy TF_VAR_name=unexpected
export TF_REATTACH_PROVIDERS=unexpected TF_DATA_DIR=unexpected TF_WORKSPACE=unexpected
isolate_offline_test_environment
[[ -z "${MOTHERDUCK_TOKEN+x}${MOTHERDUCK_ADMIN_TOKEN+x}${MOTHERDUCK_API_BASE_URL+x}${TF_CLI_ARGS+x}${TF_CLI_ARGS_plan+x}${TF_VAR_name+x}${TF_REATTACH_PROVIDERS+x}${TF_DATA_DIR+x}${TF_WORKSPACE+x}" ]]
echo 'Terraform harness tests passed'
