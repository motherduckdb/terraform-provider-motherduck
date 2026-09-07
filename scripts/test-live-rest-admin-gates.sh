#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_dir="$(mktemp -d)"
trap 'rm -rf "${test_dir}"' EXIT

mkdir -p "${test_dir}/bin"
cat >"${test_dir}/bin/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

output_file=""
while [[ "$#" -gt 0 ]]; do
  case "$1" in
    -o)
      output_file="$2"
      shift 2
      ;;
    -w)
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

if [[ -z "${output_file}" ]]; then
  echo "stub curl did not receive an output file" >&2
  exit 1
fi
printf '%s\n' '{"code":"FORBIDDEN","message":"minimum role is organization admin"}' >"${output_file}"
printf '403'
EOF
chmod +x "${test_dir}/bin/curl"

for script in \
  scripts/test-live-rest-token-matrix.sh \
  scripts/test-live-rest-edge.sh \
  scripts/test-live-complex.sh; do
  output_file="${test_dir}/$(basename "${script}").log"
  set +e
  PATH="${test_dir}/bin:${PATH}" \
    MOTHERDUCK_TOKEN=stub-sql-token \
    MOTHERDUCK_ADMIN_TOKEN=stub-admin-token \
    MOTHERDUCK_API_BASE_URL=http://stub.invalid \
    RUN_ID=nonadmin_gate \
    "${ROOT_DIR}/${script}" >"${output_file}" 2>&1
  status=$?
  set -e

  if [[ "${status}" -ne 42 ]]; then
    echo "${script} returned ${status} for a non-admin token, expected 42" >&2
    cat "${output_file}" >&2
    exit 1
  fi
  grep -F 'REST admin smoke requires an organization-admin MOTHERDUCK_ADMIN_TOKEN' "${output_file}" >/dev/null
done

echo 'REST admin caller gate tests passed'
