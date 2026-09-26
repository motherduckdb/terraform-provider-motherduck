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

# Publisher keys for CLI downloads. The fingerprints were checked against the
# publishers' own documentation: HashiCorp's binary verification guide on
# developer.hashicorp.com, and OpenTofu's installer at get.opentofu.org.
HASHICORP_RELEASE_KEY_FINGERPRINT="C874011F0AB405110D02105534365D9472D7468F"
OPENTOFU_RELEASE_KEY_FINGERPRINT="E3E6E43D84CB852EADB0051D0C0AF313E5FD9F80"

# cli_signature_verification_enabled succeeds when checksum manifests must be
# signature-checked. A checksum published next to the archive catches
# corruption but not a compromised origin, so CI always requires gpg. Local
# runs without gpg warn and keep the checksum check.
cli_signature_verification_enabled() {
  if command -v gpg >/dev/null 2>&1; then
    return 0
  fi
  if [[ "${GITHUB_ACTIONS:-}" == "true" || "${CI:-}" == "true" ]]; then
    echo "gpg is required in CI to verify CLI checksum signatures" >&2
    return 2
  fi
  echo "Warning: gpg is not installed, so the CLI checksum signature is not verified." >&2
  return 1
}

# verify_manifest_signature checks a detached signature over a checksum
# manifest and requires the signing key to belong to the expected primary key.
verify_manifest_signature() {
  local manifest_path="$1"
  local signature_path="$2"
  local public_key_path="$3"
  local expected_fingerprint="$4"

  if [[ ! -s "${public_key_path}" ]]; then
    echo "Public key ${public_key_path} is missing" >&2
    return 1
  fi

  local gpg_home
  # Short names keep the gpg-agent socket inside the unix socket path limit.
  if ! gpg_home="$(mktemp -d "${TMPDIR:-/tmp}/mdtf-gpg.XXXXXX")"; then
    echo "Failed to create a temporary gpg home" >&2
    return 1
  fi
  chmod 700 "${gpg_home}"

  local status_output="" result=0
  if ! GNUPGHOME="${gpg_home}" gpg --batch --no-tty --quiet --import "${public_key_path}" >/dev/null 2>&1; then
    echo "Failed to import ${public_key_path}" >&2
    result=1
  elif ! status_output="$(GNUPGHOME="${gpg_home}" gpg --batch --no-tty --status-fd 1 --verify "${signature_path}" "${manifest_path}" 2>/dev/null)"; then
    echo "Signature verification failed for $(basename "${manifest_path}")" >&2
    result=1
  else
    # VALIDSIG ends with the primary key fingerprint, which also covers
    # signatures made by a signing subkey.
    local primary_fingerprint
    primary_fingerprint="$(awk '$1 == "[GNUPG:]" && $2 == "VALIDSIG" { print $NF }' <<<"${status_output}")"
    if [[ "${primary_fingerprint}" != "${expected_fingerprint}" ]]; then
      echo "$(basename "${manifest_path}") is signed by ${primary_fingerprint:-an unknown key}, expected ${expected_fingerprint}" >&2
      result=1
    fi
  fi

  GNUPGHOME="${gpg_home}" gpgconf --kill all >/dev/null 2>&1 || true
  rm -rf "${gpg_home}"
  return "${result}"
}

cleanup_cli_download() {
  local download_dir="$1"
  local download_archive="$2"
  local download_manifest="$3"
  local download_signature="${4:-}"
  local cleanup_failed=false

  if [[ -e "${download_archive}" ]] && ! rm -f "${download_archive}"; then
    cleanup_failed=true
  fi
  if [[ -e "${download_manifest}" ]] && ! rm -f "${download_manifest}"; then
    cleanup_failed=true
  fi
  if [[ -n "${download_signature}" && -e "${download_signature}" ]] && ! rm -f "${download_signature}"; then
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

# download_verified_archive <archive-url> <manifest-url> <archive-path>
#   [<signature-url> <public-key-path> <expected-primary-fingerprint>]
# With the optional arguments, the checksum manifest must also carry a valid
# detached signature from the expected publisher key.
download_verified_archive() {
  local archive_url="$1"
  local manifest_url="$2"
  local archive_path="$3"
  local signature_url="${4:-}"
  local public_key_path="${5:-}"
  local expected_fingerprint="${6:-}"
  if [[ -n "${signature_url}" && ( -z "${public_key_path}" || -z "${expected_fingerprint}" ) ]]; then
    echo "A signature URL needs a public key path and an expected fingerprint" >&2
    return 1
  fi
  local check_signature=false
  if [[ -n "${signature_url}" ]]; then
    local signature_policy=0
    cli_signature_verification_enabled || signature_policy=$?
    case "${signature_policy}" in
      0) check_signature=true ;;
      1) ;;
      *) return 1 ;;
    esac
  fi
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
  local download_signature="${download_dir}/SHA256SUMS.sig"
  local -a cleanup_args=("${download_dir}" "${download_archive}" "${download_manifest}" "${download_signature}")

  if ! curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 "${manifest_url}" -o "${download_manifest}"; then
    cleanup_cli_download "${cleanup_args[@]}" || true
    return 1
  fi
  if [[ "${check_signature}" == "true" ]]; then
    if ! curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 "${signature_url}" -o "${download_signature}"; then
      cleanup_cli_download "${cleanup_args[@]}" || true
      return 1
    fi
    if ! verify_manifest_signature "${download_manifest}" "${download_signature}" "${public_key_path}" "${expected_fingerprint}"; then
      cleanup_cli_download "${cleanup_args[@]}" || true
      return 1
    fi
  fi
  if ! curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 "${archive_url}" -o "${download_archive}"; then
    cleanup_cli_download "${cleanup_args[@]}" || true
    return 1
  fi
  if ! verify_archive_checksum "${download_manifest}" "${download_archive}"; then
    cleanup_cli_download "${cleanup_args[@]}" || true
    return 1
  fi

  local staged_archive
  if ! staged_archive="$(mktemp "${archive_dir}/.${archive_name}.verified.XXXXXX")"; then
    cleanup_cli_download "${cleanup_args[@]}" || true
    return 1
  fi
  if ! mv "${download_archive}" "${staged_archive}"; then
    rm -f "${staged_archive}" || true
    cleanup_cli_download "${cleanup_args[@]}" || true
    return 1
  fi
  if ! cleanup_cli_download "${cleanup_args[@]}"; then
    rm -f "${staged_archive}" || true
    return 1
  fi
  if ! mv "${staged_archive}" "${archive_path}"; then
    rm -f "${staged_archive}" || true
    return 1
  fi
}
