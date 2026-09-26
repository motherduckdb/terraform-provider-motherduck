#!/usr/bin/env bash
# Checks that a packaged provider binary runs on the oldest supported systems.
# Keep the floors in sync with scripts/package-release.sh and the supported
# platform list in the provider documentation.

RELEASE_MAX_GLIBC="${RELEASE_MAX_GLIBC:-2.34}"
RELEASE_MAX_MACOS="${RELEASE_MAX_MACOS:-13.0}"

# release_version_le succeeds when dotted version $1 is at most $2.
release_version_le() {
  local left="$1" right="$2"
  local -a left_parts right_parts
  IFS=. read -r -a left_parts <<<"${left}"
  IFS=. read -r -a right_parts <<<"${right}"
  local count="${#left_parts[@]}"
  if [[ "${#right_parts[@]}" -gt "${count}" ]]; then
    count="${#right_parts[@]}"
  fi
  local index left_part right_part
  for ((index = 0; index < count; index++)); do
    left_part="${left_parts[index]:-0}"
    right_part="${right_parts[index]:-0}"
    if [[ ! "${left_part}" =~ ^[0-9]+$ || ! "${right_part}" =~ ^[0-9]+$ ]]; then
      echo "Cannot compare versions ${left} and ${right}" >&2
      return 2
    fi
    if ((10#${left_part} < 10#${right_part})); then
      return 0
    fi
    if ((10#${left_part} > 10#${right_part})); then
      return 1
    fi
  done
  return 0
}

# release_max_glibc_version prints the newest GLIBC_x.y symbol version that
# the objdump -T output on stdin references, or nothing.
release_max_glibc_version() {
  grep -oE 'GLIBC_[0-9]+(\.[0-9]+)+' | sed 's/^GLIBC_//' | sort -t. -k1,1n -k2,2n -k3,3n | tail -n 1
}

check_linux_release_floor() {
  local binary="$1"
  if ! command -v objdump >/dev/null 2>&1; then
    echo "objdump is required to check the glibc floor of ${binary}" >&2
    return 1
  fi
  local max_glibc
  max_glibc="$(objdump -T "${binary}" | release_max_glibc_version)"
  if [[ -z "${max_glibc}" ]]; then
    echo "No GLIBC symbol versions found in ${binary}" >&2
    return 1
  fi
  if ! release_version_le "${max_glibc}" "${RELEASE_MAX_GLIBC}"; then
    echo "${binary} requires GLIBC_${max_glibc}, newer than the supported floor GLIBC_${RELEASE_MAX_GLIBC}." >&2
    echo "Build Linux releases on a system with glibc ${RELEASE_MAX_GLIBC} or older." >&2
    return 1
  fi
  local needed
  needed="$(objdump -p "${binary}" | awk '$1 == "NEEDED" { print $2 }')"
  if grep -qE '^(libstdc\+\+|libgcc_s)\.' <<<"${needed}"; then
    echo "${binary} links the C++ runtime dynamically. Release builds must link libstdc++ and libgcc statically." >&2
    echo "${needed}" >&2
    return 1
  fi
  echo "Linux floor ok: GLIBC_${max_glibc} <= GLIBC_${RELEASE_MAX_GLIBC}, static C++ runtime"
}

check_darwin_release_floor() {
  local binary="$1"
  if ! command -v otool >/dev/null 2>&1; then
    echo "otool is required to check the macOS floor of ${binary}" >&2
    return 1
  fi
  local minos
  minos="$(otool -l "${binary}" | awk '$1 == "minos" { print $2; exit }')"
  if [[ -z "${minos}" ]]; then
    echo "No LC_BUILD_VERSION minos found in ${binary}" >&2
    return 1
  fi
  if ! release_version_le "${minos}" "${RELEASE_MAX_MACOS}"; then
    echo "${binary} requires macOS ${minos}, newer than the supported floor macOS ${RELEASE_MAX_MACOS}." >&2
    echo "Set MACOSX_DEPLOYMENT_TARGET when building release packages." >&2
    return 1
  fi
  echo "macOS floor ok: minos ${minos} <= ${RELEASE_MAX_MACOS}"
}

check_release_platform_floor() {
  local binary="$1" goos="$2"
  case "${goos}" in
    linux) check_linux_release_floor "${binary}" ;;
    darwin) check_darwin_release_floor "${binary}" ;;
    *)
      echo "No platform floor is defined for ${goos}" >&2
      return 1
      ;;
  esac
}
