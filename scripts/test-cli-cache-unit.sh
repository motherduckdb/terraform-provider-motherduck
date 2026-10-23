#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="$(mktemp -d)"
trap 'rm -rf "${fixture}"' EXIT
repo="${fixture}/repo with spaces"
mkdir -p "${repo}/scripts/lib" "${fixture}/bin"
cp "${ROOT_DIR}/scripts/test-terraform-versions.sh" "${repo}/scripts/"
for library in terraform-test live-common version-matrix; do
  cp "${ROOT_DIR}/scripts/lib/${library}.sh" "${repo}/scripts/lib/"
done

cat > "${fixture}/bin/go" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
case "$*" in
  'env GOOS') echo "${TEST_OS}" ;;
  'env GOARCH') echo "${TEST_ARCH}" ;;
  *) exit 99 ;;
esac
STUB
chmod +x "${fixture}/bin/go"

# Model verified archives for each platform, while keeping the real extraction,
# cache lookup, provider mirror, and matrix dispatch paths under test.
cat > "${repo}/scripts/lib/download-checksum.sh" <<'STUB'
HASHICORP_RELEASE_KEY_FINGERPRINT=test-hashicorp
OPENTOFU_RELEASE_KEY_FINGERPRINT=test-opentofu
download_verified_archive() {
  [[ "$#" == 6 && -n "$4" && -n "$5" && -n "$6" ]]
  local archive="${1##*/}"
  local binary="${archive%%_*}"
  local stage
  stage="$(mktemp -d "${TEST_FIXTURE}/archive.XXXXXX")"
  printf '#!/usr/bin/env bash\nprintf "%%s\\n" "%s"\n' "${archive%.zip}" > "${stage}/${binary}"
  chmod +x "${stage}/${binary}"
  mkdir -p "$(dirname "$3")"
  (cd "${stage}" && zip -q "$3" "${binary}")
  rm -rf "${stage}"
  echo "${archive}" >> "${TEST_FIXTURE}/downloads"
}
STUB
cat > "${repo}/scripts/test-cli.sh" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
actual="$("${TERRAFORM_BIN}")"
expected="$(basename "${TERRAFORM_BIN}")_${TEST_VERSION}_${TEST_OS}_${TEST_ARCH}"
if [[ "${actual}" != "${expected}" ]]; then
  echo "Wrong cached CLI: got ${actual}, expected ${expected}" >&2
  exit 1
fi
echo "${actual}" >> "${TEST_FIXTURE}/smokes"
STUB
chmod +x "${repo}/scripts/test-cli.sh"

export TEST_FIXTURE="${fixture}" PATH="${fixture}/bin:${PATH}"
export TF_TEST_PROVIDER_BINARY="${fixture}/provider"
printf '#!/usr/bin/env bash\nexit 0\n' > "${TF_TEST_PROVIDER_BINARY}"
chmod +x "${TF_TEST_PROVIDER_BINARY}"
unset PROVIDER_MIRROR_DIR
run_matrix() {
  TEST_OS="$1" TEST_ARCH="$2" TEST_VERSION="$3" \
    TF_VERSIONS="$3" TOFU_VERSIONS="$3" RUN_ID=cache-test \
    bash "${repo}/scripts/test-terraform-versions.sh" --offline
}
expect_downloads() {
  local actual
  actual="$(wc -l < "${fixture}/downloads" | tr -d ' ')"
  if [[ "${actual}" != "$1" ]]; then
    echo "CLI downloads = ${actual}, expected $1" >&2
    exit 1
  fi
}

run_matrix darwin arm64 1.2.3
expect_downloads 2
run_matrix darwin arm64 1.2.3
expect_downloads 2
# Change the OS and architecture independently, then return to the first pair.
run_matrix linux arm64 1.2.3
expect_downloads 4
run_matrix linux amd64 1.2.3
expect_downloads 6
run_matrix darwin arm64 1.2.3
expect_downloads 6
run_matrix darwin arm64 1.2.4
expect_downloads 8

# An executable without its verification marker must be installed again.
rm "${repo}/tools/terraform/1.2.3/darwin_arm64/terraform.release-verified"
run_matrix darwin arm64 1.2.3
expect_downloads 9

# Old version-only cache entries cannot establish a platform's verified binary.
for family in terraform opentofu; do
  binary=terraform
  if [[ "${family}" == opentofu ]]; then binary=tofu; fi
  legacy="${repo}/tools/${family}/1.2.5/${binary}"
  mkdir -p "$(dirname "${legacy}")"
  printf '#!/usr/bin/env bash\necho legacy-wrong-platform\n' > "${legacy}"
  chmod +x "${legacy}"
  touch "${legacy}.release-verified"
done
run_matrix darwin arm64 1.2.5
expect_downloads 11
[[ "$(wc -l < "${fixture}/smokes" | tr -d ' ')" == 16 ]]
echo 'CLI platform cache tests passed'
