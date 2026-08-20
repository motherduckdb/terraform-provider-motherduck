#!/usr/bin/env bash

rest_preflight_username() {
  local run_id="${1:-run}"
  local pid="${2:-$$}"
  local prefix="tf_rest_preflight_"
  local suffix="${run_id//[^A-Za-z0-9_]/_}"
  if [[ -z "${suffix}" ]]; then
    suffix="run"
  fi

  local max_suffix_length=$((255 - ${#prefix} - 1 - ${#pid}))
  if [[ "${max_suffix_length}" -lt 1 ]]; then
    max_suffix_length=1
  fi
  suffix="${suffix:0:${max_suffix_length}}"

  printf '%s%s_%s' "${prefix}" "${suffix}" "${pid}"
}

preflight_rest_admin() {
  if ! command -v curl >/dev/null 2>&1; then
    echo "curl is required for REST admin preflight" >&2
    return 1
  fi

  local base_url="${MOTHERDUCK_API_BASE_URL:-https://api.motherduck.com}"
  base_url="${base_url%/}"
  local run_id="${RUN_ID:-run}"
  local username
  username="$(rest_preflight_username "${run_id}" "$$")"
  local result_dir="${ROOT_DIR}/test-results"
  local body_file="${result_dir}/rest-admin-preflight-${run_id}.json"
  mkdir -p "${result_dir}"

  local status
  status="$(curl -sS -o "${body_file}" -w "%{http_code}" \
    -X POST "${base_url}/v1/users" \
    -H "Authorization: Bearer ${MOTHERDUCK_ADMIN_TOKEN}" \
    -H "Content-Type: application/json" \
    -H "Accept: application/json" \
    --data "{\"username\":\"${username}\"}")"

  if [[ "${status}" =~ ^2[0-9][0-9]$ ]]; then
    local delete_status
    delete_status="$(curl -sS -o /dev/null -w "%{http_code}" \
      -X DELETE "${base_url}/v1/users/${username}" \
      -H "Authorization: Bearer ${MOTHERDUCK_ADMIN_TOKEN}" \
      -H "Accept: application/json")"
    if [[ ! "${delete_status}" =~ ^2[0-9][0-9]$ && "${delete_status}" != "404" ]]; then
      echo "REST admin preflight created ${username}, but cleanup returned HTTP ${delete_status}" >&2
      return 1
    fi
    return 0
  fi

  if [[ "${status}" == "403" ]] && grep -Eqi 'minimum role|users\.createServiceAccount|FORBIDDEN' "${body_file}"; then
    echo "Skipping live REST admin smoke: MOTHERDUCK_ADMIN_TOKEN can authenticate, but is not an organization admin." >&2
    return 42
  fi

  echo "REST admin preflight failed with HTTP ${status}: $(tr '\n' ' ' < "${body_file}")" >&2
  return 1
}
