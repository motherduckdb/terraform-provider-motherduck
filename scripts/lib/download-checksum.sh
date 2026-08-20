#!/usr/bin/env bash

sha256_file() {
  local path="$1"
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "${path}" | awk '{print $1}'
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "${path}" | awk '{print $1}'
    return
  fi
  echo "Neither shasum nor sha256sum is available to verify downloads" >&2
  return 1
}

verify_archive_checksum() {
  local manifest_path="$1"
  local archive_path="$2"
  local archive_name
  archive_name="$(basename "${archive_path}")"

  local expected
  if ! expected="$(awk -v archive="${archive_name}" '$2 == archive { print $1; matches++ } END { if (matches != 1) exit 1 }' "${manifest_path}")"; then
    echo "Checksum manifest must contain exactly one entry for ${archive_name}" >&2
    return 1
  fi
  if [[ ! "${expected}" =~ ^[0-9a-fA-F]{64}$ ]]; then
    echo "Checksum manifest contains an invalid SHA-256 value for ${archive_name}" >&2
    return 1
  fi

  local actual
  if ! actual="$(sha256_file "${archive_path}")"; then
    return 1
  fi
  actual="$(printf '%s' "${actual}" | tr '[:upper:]' '[:lower:]')"
  expected="$(printf '%s' "${expected}" | tr '[:upper:]' '[:lower:]')"
  if [[ "${actual}" != "${expected}" ]]; then
    echo "Checksum verification failed for ${archive_name}" >&2
    return 1
  fi
}

cleanup_cli_download() {
  local download_dir="$1"
  local download_archive="$2"
  local download_manifest="$3"
  local cleanup_failed=false

  if [[ -e "${download_archive}" ]] && ! rm -f "${download_archive}"; then
    cleanup_failed=true
  fi
  if [[ -e "${download_manifest}" ]] && ! rm -f "${download_manifest}"; then
    cleanup_failed=true
  fi
  if [[ -d "${download_dir}" ]] && ! rmdir "${download_dir}"; then
    cleanup_failed=true
  fi

  if [[ "${cleanup_failed}" == "true" ]]; then
    echo "Failed to clean temporary CLI download files in ${download_dir}" >&2
    return 1
  fi
}

download_verified_archive() {
  local archive_url="$1"
  local manifest_url="$2"
  local archive_path="$3"
  local archive_name
  archive_name="$(basename "${archive_path}")"
  local archive_dir
  archive_dir="$(dirname "${archive_path}")"
  if ! mkdir -p "${archive_dir}"; then
    echo "Failed to create CLI archive directory ${archive_dir}" >&2
    return 1
  fi

  local download_dir
  if ! download_dir="$(mktemp -d "${archive_dir}/.cli-download.XXXXXX")"; then
    echo "Failed to create a temporary CLI download directory in ${archive_dir}" >&2
    return 1
  fi
  local download_archive="${download_dir}/${archive_name}"
  local download_manifest="${download_dir}/SHA256SUMS"

  if ! curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 "${manifest_url}" -o "${download_manifest}"; then
    cleanup_cli_download "${download_dir}" "${download_archive}" "${download_manifest}" || true
    return 1
  fi
  if ! curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 "${archive_url}" -o "${download_archive}"; then
    cleanup_cli_download "${download_dir}" "${download_archive}" "${download_manifest}" || true
    return 1
  fi
  if ! verify_archive_checksum "${download_manifest}" "${download_archive}"; then
    cleanup_cli_download "${download_dir}" "${download_archive}" "${download_manifest}" || true
    return 1
  fi

  local staged_archive
  if ! staged_archive="$(mktemp "${archive_dir}/.${archive_name}.verified.XXXXXX")"; then
    cleanup_cli_download "${download_dir}" "${download_archive}" "${download_manifest}" || true
    return 1
  fi
  if ! mv "${download_archive}" "${staged_archive}"; then
    rm -f "${staged_archive}" || true
    cleanup_cli_download "${download_dir}" "${download_archive}" "${download_manifest}" || true
    return 1
  fi
  if ! cleanup_cli_download "${download_dir}" "${download_archive}" "${download_manifest}"; then
    rm -f "${staged_archive}" || true
    return 1
  fi
  if ! mv "${staged_archive}" "${archive_path}"; then
    rm -f "${staged_archive}" || true
    return 1
  fi
}
