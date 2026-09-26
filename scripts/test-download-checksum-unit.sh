#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${ROOT_DIR}/scripts/lib/download-checksum.sh"

test_dir="$(mktemp -d)"
trap 'rm -rf "${test_dir}"' EXIT

archive_path="${test_dir}/terraform_1.2.3_test_arch.zip"
manifest_path="${test_dir}/SHA256SUMS"
printf 'verified archive\n' > "${archive_path}"
printf '%s  %s\n' "$(sha256_file "${archive_path}")" "$(basename "${archive_path}")" > "${manifest_path}"
verify_archive_checksum "${manifest_path}" "${archive_path}"

printf 'tampered archive\n' > "${archive_path}"
if verify_archive_checksum "${manifest_path}" "${archive_path}" 2>/dev/null; then
  echo "Expected a tampered archive to fail checksum verification" >&2
  exit 1
fi

printf '%064d  another_archive.zip\n' 0 > "${manifest_path}"
if verify_archive_checksum "${manifest_path}" "${archive_path}" 2>/dev/null; then
  echo "Expected a missing archive entry to fail checksum verification" >&2
  exit 1
fi

printf '%s  %s\n%s  %s\n' \
  "$(sha256_file "${archive_path}")" "$(basename "${archive_path}")" \
  "$(sha256_file "${archive_path}")" "$(basename "${archive_path}")" > "${manifest_path}"
if verify_archive_checksum "${manifest_path}" "${archive_path}" 2>/dev/null; then
  echo "Expected duplicate archive entries to fail checksum verification" >&2
  exit 1
fi

printf 'not-a-digest  %s\n' "$(basename "${archive_path}")" > "${manifest_path}"
if verify_archive_checksum "${manifest_path}" "${archive_path}" 2>/dev/null; then
  echo "Expected a malformed checksum to fail verification" >&2
  exit 1
fi

mkdir -p "${test_dir}/empty-path"
if PATH="${test_dir}/empty-path" sha256_file "${archive_path}" 2>/dev/null; then
  echo "Expected verification without a checksum utility to fail" >&2
  exit 1
fi

fixture_archive_path="${test_dir}/download-source.zip"
fixture_manifest_path="${test_dir}/download-source.SHA256SUMS"
printf 'downloaded archive\n' > "${fixture_archive_path}"
printf '%s  %s\n' "$(sha256_file "${fixture_archive_path}")" "published.zip" > "${fixture_manifest_path}"

curl() {
  local url=""
  local output=""
  while [[ "$#" -gt 0 ]]; do
    case "$1" in
      -o)
        output="$2"
        shift 2
        ;;
      https://*)
        url="$1"
        shift
        ;;
      *)
        shift
        ;;
    esac
  done
  if [[ "${FAKE_CURL_FAILURE:-}" == "manifest" && "${url}" == *SHA256SUMS ]]; then
    return 22
  fi
  if [[ "${FAKE_CURL_FAILURE:-}" == "archive" && "${url}" != *SHA256SUMS ]]; then
    return 22
  fi
  if [[ "${FAKE_CURL_FAILURE:-}" == "signature" && "${url}" == *.sig ]]; then
    return 22
  fi
  if [[ "${url}" == *.sig ]]; then
    cp "${fixture_signature_path}" "${output}"
  elif [[ "${url}" == *SHA256SUMS ]]; then
    cp "${fixture_manifest_path}" "${output}"
  elif [[ "${FAKE_CURL_TAMPER:-}" == "1" ]]; then
    printf 'substituted archive\n' > "${output}"
  else
    cp "${fixture_archive_path}" "${output}"
  fi
}

published_archive="${test_dir}/published.zip"
download_verified_archive "https://example.test/published.zip" "https://example.test/SHA256SUMS" "${published_archive}"
verify_archive_checksum "${fixture_manifest_path}" "${published_archive}"

rm -f "${published_archive}"
FAKE_CURL_FAILURE=manifest
if download_verified_archive "https://example.test/published.zip" "https://example.test/SHA256SUMS" "${published_archive}" 2>/dev/null; then
  echo "Expected a failed manifest download to abort" >&2
  exit 1
fi
if [[ -e "${published_archive}" ]]; then
  echo "Failed manifest download published an artifact" >&2
  exit 1
fi

FAKE_CURL_FAILURE=archive
if download_verified_archive "https://example.test/published.zip" "https://example.test/SHA256SUMS" "${published_archive}" 2>/dev/null; then
  echo "Expected a failed archive download to abort" >&2
  exit 1
fi
if [[ -e "${published_archive}" ]]; then
  echo "Failed archive download published an artifact" >&2
  exit 1
fi

unset FAKE_CURL_FAILURE
FAKE_CURL_TAMPER=1
if download_verified_archive "https://example.test/published.zip" "https://example.test/SHA256SUMS" "${published_archive}" 2>/dev/null; then
  echo "Expected a substituted archive to fail checksum verification" >&2
  exit 1
fi
if [[ -e "${published_archive}" ]]; then
  echo "Checksum mismatch published an artifact" >&2
  exit 1
fi

printf 'existing verified archive\n' > "${published_archive}"
existing_checksum="$(sha256_file "${published_archive}")"
if download_verified_archive "https://example.test/published.zip" "https://example.test/SHA256SUMS" "${published_archive}" 2>/dev/null; then
  echo "Expected a failed replacement to abort" >&2
  exit 1
fi
if [[ "$(sha256_file "${published_archive}")" != "${existing_checksum}" ]]; then
  echo "Failed replacement changed the existing archive" >&2
  exit 1
fi

if find "${test_dir}" -maxdepth 1 -type d -name '.cli-download.*' | grep -q .; then
  echo "CLI download test left a temporary directory behind" >&2
  exit 1
fi
if find "${test_dir}" -maxdepth 1 -type f -name '.published.zip.verified.*' | grep -q .; then
  echo "CLI download test left a staged archive behind" >&2
  exit 1
fi

# Signature verification. Generate a throwaway publisher key so the test never
# depends on the network or on a real publisher key.
if ! command -v gpg >/dev/null 2>&1; then
  echo "gpg is required to test CLI checksum signatures (brew install gnupg / apt-get install gnupg)" >&2
  exit 1
fi
# Short names keep the gpg-agent socket inside the unix socket path limit.
publisher_home="$(mktemp -d "${TMPDIR:-/tmp}/mdtf-pub.XXXXXX")"
other_home="$(mktemp -d "${TMPDIR:-/tmp}/mdtf-oth.XXXXXX")"
chmod 700 "${publisher_home}" "${other_home}"
cleanup_signature_test() {
  GNUPGHOME="${publisher_home}" gpgconf --kill all >/dev/null 2>&1 || true
  GNUPGHOME="${other_home}" gpgconf --kill all >/dev/null 2>&1 || true
  rm -rf "${publisher_home}" "${other_home}" "${test_dir}"
}
trap cleanup_signature_test EXIT

generate_key() {
  local home="$1"
  local uid="$2"
  GNUPGHOME="${home}" gpg --batch --no-tty --quiet --passphrase '' --pinentry-mode loopback \
    --quick-gen-key "${uid}" ed25519 sign never >/dev/null 2>&1
  GNUPGHOME="${home}" gpg --batch --with-colons --fingerprint 2>/dev/null | awk -F: '$1 == "fpr" { print $10; exit }'
}
publisher_fingerprint="$(generate_key "${publisher_home}" "Test Publisher <publisher@example.test>")"
other_fingerprint="$(generate_key "${other_home}" "Other Publisher <other@example.test>")"
publisher_key="${test_dir}/publisher.asc"
other_key="${test_dir}/other.asc"
GNUPGHOME="${publisher_home}" gpg --batch --armor --export "${publisher_fingerprint}" > "${publisher_key}"
GNUPGHOME="${other_home}" gpg --batch --armor --export "${other_fingerprint}" > "${other_key}"

fixture_signature_path="${test_dir}/download-source.SHA256SUMS.sig"
GNUPGHOME="${publisher_home}" gpg --batch --no-tty --quiet --detach-sign \
  --output "${fixture_signature_path}" "${fixture_manifest_path}"

verify_manifest_signature "${fixture_manifest_path}" "${fixture_signature_path}" "${publisher_key}" "${publisher_fingerprint}"

if verify_manifest_signature "${fixture_manifest_path}" "${fixture_signature_path}" "${other_key}" "${other_fingerprint}" 2>/dev/null; then
  echo "Expected a signature from another key to fail verification" >&2
  exit 1
fi
if verify_manifest_signature "${fixture_manifest_path}" "${fixture_signature_path}" "${publisher_key}" "${other_fingerprint}" 2>/dev/null; then
  echo "Expected an unexpected signer fingerprint to fail verification" >&2
  exit 1
fi
cp "${fixture_manifest_path}" "${test_dir}/tampered.SHA256SUMS"
printf '%064d  extra.zip\n' 0 >> "${test_dir}/tampered.SHA256SUMS"
if verify_manifest_signature "${test_dir}/tampered.SHA256SUMS" "${fixture_signature_path}" "${publisher_key}" "${publisher_fingerprint}" 2>/dev/null; then
  echo "Expected a modified manifest to fail signature verification" >&2
  exit 1
fi

unset FAKE_CURL_FAILURE FAKE_CURL_TAMPER
rm -f "${published_archive}"
download_verified_archive "https://example.test/published.zip" "https://example.test/SHA256SUMS" "${published_archive}" \
  "https://example.test/SHA256SUMS.sig" "${publisher_key}" "${publisher_fingerprint}"
verify_archive_checksum "${fixture_manifest_path}" "${published_archive}"

rm -f "${published_archive}"
if download_verified_archive "https://example.test/published.zip" "https://example.test/SHA256SUMS" "${published_archive}" \
  "https://example.test/SHA256SUMS.sig" "${publisher_key}" "${other_fingerprint}" 2>/dev/null; then
  echo "Expected a download signed by an unexpected key to abort" >&2
  exit 1
fi
if [[ -e "${published_archive}" ]]; then
  echo "Signature mismatch published an artifact" >&2
  exit 1
fi

FAKE_CURL_FAILURE=signature
if download_verified_archive "https://example.test/published.zip" "https://example.test/SHA256SUMS" "${published_archive}" \
  "https://example.test/SHA256SUMS.sig" "${publisher_key}" "${publisher_fingerprint}" 2>/dev/null; then
  echo "Expected a failed signature download to abort" >&2
  exit 1
fi
unset FAKE_CURL_FAILURE
if [[ -e "${published_archive}" ]]; then
  echo "Failed signature download published an artifact" >&2
  exit 1
fi

# Without gpg, CI must fail and local runs fall back to the checksum.
mkdir -p "${test_dir}/no-gpg"
for tool in awk basename cat chmod cp dirname mkdir mktemp mv rm rmdir shasum sha256sum tr; do
  if command -v "${tool}" >/dev/null 2>&1; then
    ln -s "$(command -v "${tool}")" "${test_dir}/no-gpg/${tool}"
  fi
done
if PATH="${test_dir}/no-gpg" GITHUB_ACTIONS=true download_verified_archive "https://example.test/published.zip" "https://example.test/SHA256SUMS" "${published_archive}" \
  "https://example.test/SHA256SUMS.sig" "${publisher_key}" "${publisher_fingerprint}" 2>/dev/null; then
  echo "Expected CI downloads without gpg to fail" >&2
  exit 1
fi
PATH="${test_dir}/no-gpg" GITHUB_ACTIONS="" CI="" download_verified_archive "https://example.test/published.zip" "https://example.test/SHA256SUMS" "${published_archive}" \
  "https://example.test/SHA256SUMS.sig" "${publisher_key}" "${publisher_fingerprint}" 2>/dev/null
verify_archive_checksum "${fixture_manifest_path}" "${published_archive}"

for key_file in scripts/keys/hashicorp-security.asc:"${HASHICORP_RELEASE_KEY_FINGERPRINT}" scripts/keys/opentofu.asc:"${OPENTOFU_RELEASE_KEY_FINGERPRINT}"; do
  key_path="${ROOT_DIR}/${key_file%%:*}"
  expected="${key_file#*:}"
  actual="$(gpg --batch --with-colons --show-keys "${key_path}" 2>/dev/null | awk -F: '$1 == "fpr" { print $10; exit }')"
  if [[ "${actual}" != "${expected}" ]]; then
    echo "${key_file%%:*} has primary fingerprint ${actual}, expected ${expected}" >&2
    exit 1
  fi
done

if find "${test_dir}" -maxdepth 1 -type d -name '.cli-download.*' | grep -q .; then
  echo "Signed CLI download test left a temporary directory behind" >&2
  exit 1
fi
