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

echo "==> Building ${binary_name} for ${TARGET_OS}_${TARGET_ARCH}"
(
  cd "${ROOT_DIR}"
  CGO_ENABLED=1 GOOS="${TARGET_OS}" GOARCH="${TARGET_ARCH}" go build \
    -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o "${work_dir}/${binary_name}" \
    .
)

echo "==> Packaging ${archive_name}"
(
  cd "${work_dir}"
  zip -q "${DIST_DIR}/${archive_name}" "${binary_name}"
)

echo "${DIST_DIR}/${archive_name}"
