#!/usr/bin/env bash
set -euo pipefail

# Hermetic checks for scripts/sign-release.sh. Generates a throwaway signing key
# in an isolated GNUPGHOME, then asserts the produced signature is detached,
# binary, and verifiable, and that the documented failure modes fail.

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if ! command -v gpg >/dev/null 2>&1; then
  echo "gpg is required to test release signing (brew install gnupg / apt-get install gnupg)" >&2
  exit 1
fi

# Short names keep the gpg-agent socket inside the unix socket path limit.
test_dir="$(mktemp -d "${TMPDIR:-/tmp}/mdtf-signtest.XXXXXX")"
verify_home="${test_dir}/v"
generate_home="${test_dir}/g"
mkdir -p "${verify_home}" "${generate_home}"
chmod 700 "${verify_home}" "${generate_home}"
cleanup() {
  GNUPGHOME="${generate_home}" gpg-connect-agent killagent /bye >/dev/null 2>&1 || true
  GNUPGHOME="${verify_home}" gpg-connect-agent killagent /bye >/dev/null 2>&1 || true
  rm -rf "${test_dir}"
}
trap cleanup EXIT

passphrase="release-signing-test-passphrase"

GNUPGHOME="${generate_home}" gpg --batch --yes --no-tty --pinentry-mode loopback \
  --passphrase "${passphrase}" \
  --quick-generate-key "Release Signing Test <release-signing-test@example.invalid>" \
  default default never >/dev/null 2>&1

fingerprint="$(
  GNUPGHOME="${generate_home}" gpg --batch --list-secret-keys --with-colons 2>/dev/null |
    awk -F: '
      $1 == "sec" { want = 1; next }
      $1 == "fpr" && want { print $10; want = 0 }
    '
)"
if [[ -z "${fingerprint}" ]]; then
  echo "Failed to generate a throwaway signing key" >&2
  exit 1
fi

private_key="$(
  GNUPGHOME="${generate_home}" gpg --batch --no-tty --pinentry-mode loopback \
    --passphrase "${passphrase}" --armor --export-secret-keys "${fingerprint}"
)"
GNUPGHOME="${generate_home}" gpg --batch --armor --export "${fingerprint}" \
  > "${test_dir}/public.asc" 2>/dev/null
GNUPGHOME="${verify_home}" gpg --batch --import "${test_dir}/public.asc" >/dev/null 2>&1

checksum_file="${test_dir}/terraform-provider-motherduck_0.0.1_SHA256SUMS"
printf '%064d  terraform-provider-motherduck_0.0.1_linux_amd64.zip\n' 0 > "${checksum_file}"
signature_file="${checksum_file}.sig"

GPG_PRIVATE_KEY="${private_key}" \
  GPG_PASSPHRASE="${passphrase}" \
  GPG_FINGERPRINT="${fingerprint}" \
  "${ROOT_DIR}/scripts/sign-release.sh" "${checksum_file}" >/dev/null

test -s "${signature_file}"

# The Registry rejects an ASCII-armored signature.
if head -c 30 "${signature_file}" | grep -q -- "-----BEGIN"; then
  echo "Expected a binary detached signature, got an ASCII-armored one" >&2
  exit 1
fi

# The signature must verify against the published checksum file with only the
# public key available, exactly as a consumer would verify it.
GNUPGHOME="${verify_home}" gpg --batch --verify "${signature_file}" "${checksum_file}" >/dev/null 2>&1

printf '%064d  tampered.zip\n' 1 > "${test_dir}/tampered"
if GNUPGHOME="${verify_home}" gpg --batch --verify "${signature_file}" "${test_dir}/tampered" >/dev/null 2>&1; then
  echo "Expected a tampered checksum file to fail signature verification" >&2
  exit 1
fi

if GPG_PRIVATE_KEY="" GPG_PASSPHRASE="${passphrase}" \
  "${ROOT_DIR}/scripts/sign-release.sh" "${checksum_file}" >/dev/null 2>&1; then
  echo "Expected a missing GPG_PRIVATE_KEY to fail signing" >&2
  exit 1
fi

if GPG_PRIVATE_KEY="${private_key}" GPG_PASSPHRASE="wrong-passphrase" \
  "${ROOT_DIR}/scripts/sign-release.sh" "${checksum_file}" >/dev/null 2>&1; then
  echo "Expected a wrong passphrase to fail signing" >&2
  exit 1
fi

if GPG_PRIVATE_KEY="${private_key}" GPG_PASSPHRASE="${passphrase}" \
  GPG_FINGERPRINT="0000000000000000000000000000000000000000" \
  "${ROOT_DIR}/scripts/sign-release.sh" "${checksum_file}" >/dev/null 2>&1; then
  echo "Expected an unexpected fingerprint to fail signing" >&2
  exit 1
fi

# The Registry shows a long key ID, not a full fingerprint, so both forms must
# be accepted, and so must lowercase and spaced input.
long_key_id="${fingerprint: -16}"
GPG_PRIVATE_KEY="${private_key}" GPG_PASSPHRASE="${passphrase}" \
  GPG_FINGERPRINT="${long_key_id}" \
  "${ROOT_DIR}/scripts/sign-release.sh" "${checksum_file}" >/dev/null
GNUPGHOME="${verify_home}" gpg --batch --verify "${signature_file}" "${checksum_file}" >/dev/null 2>&1

GPG_PRIVATE_KEY="${private_key}" GPG_PASSPHRASE="${passphrase}" \
  GPG_FINGERPRINT="$(printf '%s' "${long_key_id}" | tr '[:upper:]' '[:lower:]')" \
  "${ROOT_DIR}/scripts/sign-release.sh" "${checksum_file}" >/dev/null

GPG_PRIVATE_KEY="${private_key}" GPG_PASSPHRASE="${passphrase}" \
  GPG_FINGERPRINT="0x${long_key_id}" \
  "${ROOT_DIR}/scripts/sign-release.sh" "${checksum_file}" >/dev/null

# A wrong long key ID of the right shape must still fail.
if GPG_PRIVATE_KEY="${private_key}" GPG_PASSPHRASE="${passphrase}" \
  GPG_FINGERPRINT="0000000000000000" \
  "${ROOT_DIR}/scripts/sign-release.sh" "${checksum_file}" >/dev/null 2>&1; then
  echo "Expected a wrong long key ID to fail signing" >&2
  exit 1
fi

# Too short to be collision resistant, and not hex at all, must both be refused.
for bad in "2CBF0A1C" "not-a-fingerprint"; do
  if GPG_PRIVATE_KEY="${private_key}" GPG_PASSPHRASE="${passphrase}" \
    GPG_FINGERPRINT="${bad}" \
    "${ROOT_DIR}/scripts/sign-release.sh" "${checksum_file}" >/dev/null 2>&1; then
    echo "Expected GPG_FINGERPRINT '${bad}' to be refused" >&2
    exit 1
  fi
done

if GPG_PRIVATE_KEY="${private_key}" GPG_PASSPHRASE="${passphrase}" \
  "${ROOT_DIR}/scripts/sign-release.sh" "${test_dir}/missing_SHA256SUMS" >/dev/null 2>&1; then
  echo "Expected a missing checksum file to fail signing" >&2
  exit 1
fi

echo "release signing checks passed"
