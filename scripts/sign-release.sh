#!/usr/bin/env bash
set -euo pipefail

# Produce the detached binary GPG signature the Terraform Registry requires for
# a release checksum file. The Registry rejects ASCII-armored signatures, so the
# signature is written in gpg's default binary form and re-verified here.
#
# Usage: scripts/sign-release.sh <checksum-file>
#
# Environment:
#   GPG_PRIVATE_KEY  Required. ASCII-armored private key for the publisher.
#   GPG_PASSPHRASE   Optional. Passphrase for that key, empty when unprotected.
#   GPG_FINGERPRINT  Optional. Expected key, asserted when set. Accepts the full
#                    40-character fingerprint or a long key ID (the last 16 or
#                    more hex characters of it), case and spacing insensitive.

checksum_file="${1:-}"
if [[ -z "${checksum_file}" ]]; then
  echo "Usage: scripts/sign-release.sh <checksum-file>" >&2
  exit 1
fi

if [[ ! -f "${checksum_file}" ]]; then
  echo "Checksum file ${checksum_file} does not exist." >&2
  exit 1
fi

if [[ -z "${GPG_PRIVATE_KEY:-}" ]]; then
  echo "GPG_PRIVATE_KEY is required and must hold an ASCII-armored private key." >&2
  exit 1
fi

if ! command -v gpg >/dev/null 2>&1; then
  echo "gpg is required (brew install gnupg / apt-get install gnupg)" >&2
  exit 1
fi

signature_file="${checksum_file}.sig"
# Keep this directory name short. gpg-agent's socket lives inside GNUPGHOME and
# a long path exceeds the platform's unix socket limit.
key_home="$(mktemp -d "${TMPDIR:-/tmp}/mdtf-sign.XXXXXX")"
chmod 700 "${key_home}"
cleanup() {
  GNUPGHOME="${key_home}" gpg-connect-agent killagent /bye >/dev/null 2>&1 || true
  rm -rf "${key_home}"
}
trap cleanup EXIT

gpg_batch() {
  GNUPGHOME="${key_home}" gpg --batch --yes --no-tty --pinentry-mode loopback "$@"
}

printf '%s\n' "${GPG_PRIVATE_KEY}" > "${key_home}/private.asc"
printf '%s' "${GPG_PASSPHRASE:-}" > "${key_home}/passphrase"
chmod 600 "${key_home}/private.asc" "${key_home}/passphrase"

if ! gpg_batch --passphrase-file "${key_home}/passphrase" \
  --import "${key_home}/private.asc" >/dev/null 2>&1; then
  echo "Failed to import the signing key from GPG_PRIVATE_KEY." >&2
  exit 1
fi

# Only primary-key fingerprints matter here. In gpg's colon output the primary
# fingerprint is the first "fpr" record after each "sec" record. Later ones
# belong to subkeys.
fingerprints="$(
  gpg_batch --list-secret-keys --with-colons 2>/dev/null |
    awk -F: '
      $1 == "sec" { want = 1; next }
      $1 == "fpr" && want { print $10; want = 0 }
    ' | sort -u
)"
fingerprint_count="$(printf '%s' "${fingerprints}" | grep -c . || true)"
if [[ "${fingerprint_count}" -ne 1 ]]; then
  echo "Expected exactly one signing key in GPG_PRIVATE_KEY, found ${fingerprint_count}." >&2
  exit 1
fi
fingerprint="${fingerprints}"

if [[ -n "${GPG_FINGERPRINT:-}" ]]; then
  # The Registry displays a long key ID rather than a full fingerprint, so
  # accept either. A long key ID is the trailing 16 hex characters of the
  # fingerprint. Shorter values are refused because short key IDs collide.
  expected="$(printf '%s' "${GPG_FINGERPRINT}" | tr -d '[:space:]' | tr '[:lower:]' '[:upper:]')"
  expected="${expected#0X}"
  if [[ ! "${expected}" =~ ^[0-9A-F]{16,40}$ ]]; then
    echo "GPG_FINGERPRINT must be 16 to 40 hex characters, a long key ID or a full fingerprint." >&2
    exit 1
  fi
  if [[ "${fingerprint}" != *"${expected}" ]]; then
    echo "Signing key ${fingerprint} does not match GPG_FINGERPRINT ${expected}." >&2
    exit 1
  fi
fi

echo "==> Signing $(basename "${checksum_file}") with ${fingerprint}"
rm -f "${signature_file}"
gpg_batch \
  --passphrase-file "${key_home}/passphrase" \
  --local-user "${fingerprint}" \
  --detach-sign \
  --output "${signature_file}" \
  "${checksum_file}"

# The Registry requires a binary signature. Fail loudly rather than publishing
# an armored file that would be rejected after the release is already created.
if head -c 30 "${signature_file}" | grep -q -- "-----BEGIN"; then
  echo "Signature ${signature_file} is ASCII-armored. The Registry requires a binary signature." >&2
  exit 1
fi

if ! gpg_batch --verify "${signature_file}" "${checksum_file}" >/dev/null 2>&1; then
  echo "Failed to verify ${signature_file} against ${checksum_file}." >&2
  exit 1
fi

echo "${signature_file}"
