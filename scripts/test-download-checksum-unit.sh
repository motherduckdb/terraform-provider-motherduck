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
  if [[ "${url}" == *SHA256SUMS ]]; then
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
