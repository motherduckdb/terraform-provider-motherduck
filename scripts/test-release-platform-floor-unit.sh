#!/usr/bin/env bash
set -euo pipefail

# Hermetic checks for scripts/lib/release-platform-floor.sh.

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/release-platform-floor.sh
source "${ROOT_DIR}/scripts/lib/release-platform-floor.sh"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

expect_le() {
  release_version_le "$1" "$2" || fail "expected $1 <= $2"
}

expect_gt() {
  if release_version_le "$1" "$2"; then
    fail "expected $1 > $2"
  fi
}

expect_le 2.34 2.34
expect_le 2.17 2.34
expect_le 2.9 2.34
expect_le 13 13.0
expect_le 12.7.1 13.0
expect_gt 2.38 2.34
expect_gt 15.0 13.0
expect_gt 13.0.1 13.0

set +e
release_version_le 2.x 2.34 2>/dev/null
status=$?
set -e
[[ "${status}" -eq 2 ]] || fail "non-numeric versions must return 2, got ${status}"

objdump_sample='
0000000000000000      DF *UND*  0000000000000000 (GLIBC_2.17) fmod
0000000000000000      DF *UND*  0000000000000000 (GLIBC_2.38) fmodf
0000000000000000      DF *UND*  0000000000000000 (GLIBC_2.9) pipe2
0000000000000000      DF *UND*  0000000000000000 (GLIBCXX_3.4.30) _ZSt4cout
0000000000000000      DF *UND*  0000000000000000 (GLIBC_2.34) pthread_create'
max="$(release_max_glibc_version <<<"${objdump_sample}")"
[[ "${max}" == "2.38" ]] || fail "expected max glibc 2.38, got ${max}"

set +e
check_release_platform_floor /nonexistent windows 2>/dev/null
status=$?
set -e
[[ "${status}" -ne 0 ]] || fail "unsupported platforms must fail"

# Check the host toolchain's own binaries when the inspection tool exists.
case "$(uname -s)" in
  Darwin)
    if command -v otool >/dev/null 2>&1; then
      RELEASE_MAX_MACOS=99.0 check_darwin_release_floor /bin/ls >/dev/null
      set +e
      RELEASE_MAX_MACOS=1.0 check_darwin_release_floor /bin/ls 2>/dev/null
      status=$?
      set -e
      [[ "${status}" -ne 0 ]] || fail "macOS floor 1.0 must reject /bin/ls"
    fi
    ;;
  Linux)
    if command -v objdump >/dev/null 2>&1; then
      set +e
      RELEASE_MAX_GLIBC=2.0 check_linux_release_floor /bin/ls 2>/dev/null
      status=$?
      set -e
      [[ "${status}" -ne 0 ]] || fail "glibc floor 2.0 must reject /bin/ls"
    fi
    ;;
esac

echo "PASS release platform floor"
