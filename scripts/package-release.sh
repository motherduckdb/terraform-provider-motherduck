#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

VERSION="${VERSION:-}"
TARGET_OS="${GOOS:-$(go env GOOS)}"
TARGET_ARCH="${GOARCH:-$(go env GOARCH)}"
DIST_DIR="${DIST_DIR:-${ROOT_DIR}/dist}"
PROVIDER_NAME="terraform-provider-motherduck"

if [[ -z "${VERSION}" ]]; then
  echo "VERSION is required, for example VERSION=0.1.0" >&2
  exit 1
fi

VERSION="${VERSION#v}"
if [[ ! "${VERSION}" =~ ^[0-9]+[.][0-9]+[.][0-9]+([.-][0-9A-Za-z][0-9A-Za-z.-]*)?$ ]]; then
  echo "VERSION must be a semantic version without build metadata, got '${VERSION}'" >&2
  exit 1
fi

case "${TARGET_OS}_${TARGET_ARCH}" in
  linux_amd64|linux_arm64|darwin_amd64|darwin_arm64)
    ;;
  *)
    echo "Unsupported release target ${TARGET_OS}_${TARGET_ARCH}" >&2
    echo "Supported targets are linux_amd64, linux_arm64, darwin_amd64, and darwin_arm64." >&2
    exit 1
    ;;
esac

mkdir -p "${DIST_DIR}"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/${PROVIDER_NAME}.XXXXXX")"
trap 'rm -rf "${work_dir}"' EXIT

binary_name="${PROVIDER_NAME}_v${VERSION}"
archive_name="${PROVIDER_NAME}_${VERSION}_${TARGET_OS}_${TARGET_ARCH}.zip"

# Platform floors. Keep these in sync with scripts/test-release-package.sh
# and the supported platform list in the provider documentation.
macos_deployment_target="${MACOSX_DEPLOYMENT_TARGET:-13.0}"

cgo_cflags="${CGO_CFLAGS:--O2 -g}"
cgo_cxxflags="${CGO_CXXFLAGS:--O2 -g}"
cgo_ldflags="${CGO_LDFLAGS:--O2 -g}"
case "${TARGET_OS}" in
  darwin)
    # Go 1.27 supports macOS 13 and later, and the prebuilt DuckDB archives
    # target macOS 11. Without an explicit target, clang stamps the build
    # host's macOS version into the binary. Passing the flag through CGO_*
    # also keeps cached cgo objects built for another target out of the link.
    export MACOSX_DEPLOYMENT_TARGET="${macos_deployment_target}"
    cgo_cflags="${cgo_cflags} -mmacosx-version-min=${macos_deployment_target}"
    cgo_cxxflags="${cgo_cxxflags} -mmacosx-version-min=${macos_deployment_target}"
    cgo_ldflags="${cgo_ldflags} -mmacosx-version-min=${macos_deployment_target}"
    ;;
  linux)
    # duckdb-go-bindings links libstdc++ with an explicit -lstdc++, which
    # -static-libstdc++ does not affect. A search directory that holds only
    # the static archive makes the linker choose it, so the binary does not
    # depend on the build host's GLIBCXX symbol versions.
    cxx="${CXX:-g++}"
    libstdcxx_archive="$("${cxx}" -print-file-name=libstdc++.a)"
    if [[ "${libstdcxx_archive}" != /* || ! -f "${libstdcxx_archive}" ]]; then
      echo "Static libstdc++.a was not found through ${cxx}. Install the libstdc++ development package." >&2
      exit 1
    fi
    mkdir -p "${work_dir}/static-libstdcxx"
    ln -s "${libstdcxx_archive}" "${work_dir}/static-libstdcxx/libstdc++.a"
    cgo_ldflags="${cgo_ldflags} -L${work_dir}/static-libstdcxx -static-libgcc"
    ;;
esac

echo "==> Building ${binary_name} for ${TARGET_OS}_${TARGET_ARCH}"
(
  cd "${ROOT_DIR}"
  CGO_ENABLED=1 CGO_CFLAGS="${cgo_cflags}" CGO_CXXFLAGS="${cgo_cxxflags}" CGO_LDFLAGS="${cgo_ldflags}" \
    GOOS="${TARGET_OS}" GOARCH="${TARGET_ARCH}" go build \
    -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o "${work_dir}/${binary_name}" \
    .
)

# Fix the archive metadata so the same binary always produces the same zip.
# SOURCE_DATE_EPOCH follows the reproducible-builds convention. The fallback
# is the commit time, or the earliest time a zip entry can record.
if [[ -z "${SOURCE_DATE_EPOCH:-}" ]]; then
  SOURCE_DATE_EPOCH="$(git -C "${ROOT_DIR}" log -1 --format=%ct 2>/dev/null || echo 315532800)"
fi
if [[ ! "${SOURCE_DATE_EPOCH}" =~ ^[0-9]+$ ]]; then
  echo "SOURCE_DATE_EPOCH must be a Unix timestamp, got '${SOURCE_DATE_EPOCH}'" >&2
  exit 1
fi
archive_mtime="$(date -u -r "${SOURCE_DATE_EPOCH}" +%Y%m%d%H%M.%S 2>/dev/null || date -u -d "@${SOURCE_DATE_EPOCH}" +%Y%m%d%H%M.%S)"
chmod 0755 "${work_dir}/${binary_name}"
TZ=UTC touch -t "${archive_mtime}" "${work_dir}/${binary_name}"

echo "==> Packaging ${archive_name}"
rm -f "${DIST_DIR}/${archive_name}"
(
  cd "${work_dir}"
  TZ=UTC zip -q -X "${DIST_DIR}/${archive_name}" "${binary_name}"
)

echo "${DIST_DIR}/${archive_name}"
