#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

cd "${ROOT_DIR}"

search_repository() {
  local pattern="$1"
  local case_mode="${2:-}"

  if command -v rg >/dev/null 2>&1; then
    if [[ "${case_mode}" == "ignore-case" ]]; then
      rg -n -i "${pattern}" \
        --glob '!dist/**' \
        --glob '!test-results/**' \
        --glob '!tools/**' \
        --glob '!**/.terraform/**' \
        --glob '!private/**' \
        .
    else
      rg -n "${pattern}" \
        --glob '!dist/**' \
        --glob '!test-results/**' \
        --glob '!tools/**' \
        --glob '!**/.terraform/**' \
        --glob '!private/**' \
        .
    fi
    return
  fi

  local grep_flags=(-nIE)
  if [[ "${case_mode}" == "ignore-case" ]]; then
    grep_flags=(-nIEi)
  fi

  find . \
    \( \
      -path './.git' -o \
      -path './dist' -o \
      -path './test-results' -o \
      -path './tools' -o \
      -path './private' -o \
      -path '*/.terraform' \
    \) -prune -o \
    -type f -print0 | xargs -0 grep "${grep_flags[@]}" "${pattern}" || true
}

check_no_search_matches() {
  local description="$1"
  local pattern="$2"
  local case_mode="${3:-}"

  local output
  set +e
  output="$(search_repository "${pattern}" "${case_mode}" 2>&1)"
  local status=$?
  set -e

  if [[ "${status}" -eq 0 && -n "${output}" ]]; then
    echo "${description}" >&2
    printf '%s\n' "${output}" >&2
    exit 1
  fi
  if [[ "${status}" -gt 1 ]]; then
    printf '%s\n' "${output}" >&2
    exit "${status}"
  fi
}

check_no_search_matches \
  "JWT-like token found outside ignored generated/private paths:" \
  'eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+'

check_no_search_matches \
  "Unexpected legacy provider reference found outside ignored generated/private paths:" \
  '[s]nowflake|[s]nowflakedb' \
  ignore-case

if rg -n 't[.](Skip|Skipf)[(]' internal --glob '*_test.go'; then
  echo "Go tests must use explicit build tags and hard preconditions instead of reporting skipped gates." >&2
  exit 1
fi

while IFS= read -r script_path; do
  [[ "${script_path}" == *"*"* ]] && continue
  script_path="${script_path#./}"
  if [[ ! -f "${script_path}" ]]; then
    echo "Makefile references missing script: ${script_path}" >&2
    exit 1
  fi
done < <(
  perl -ne 'while (m{(?:\./)?scripts/[A-Za-z0-9_./*-]+[.]sh}g) { print "$&\n" }' Makefile | sort -u
)

while IFS=$'\t' read -r file line target; do
  case "${target}" in
    ""|\#*|http://*|https://*|mailto:*)
      continue
      ;;
  esac

  target="${target%%#*}"
  target="${target#<}"
  target="${target%>}"

  local_path="$(dirname "${file}")/${target}"
  if [[ ! -e "${local_path}" ]]; then
    echo "Broken local Markdown link in ${file}:${line}: ${target}" >&2
    exit 1
  fi
done < <(
  find README.md docs examples -type f -name '*.md' -print | sort | while IFS= read -r file; do
    perl -ne 'while (/\[[^\]]+\]\(([^)]+)\)/g) { print "$ARGV\t$.\t$1\n" }' "${file}"
  done
)

while IFS= read -r script_path; do
  if perl -0ne 'if (/cleanup\n\s*trap - EXIT/) { $found = 1 } END { exit !$found }' "${script_path}"; then
    echo "Unsafe cleanup ordering in ${script_path}: disable the EXIT trap before explicit cleanup so a cleanup failure does not run twice." >&2
    exit 1
  fi
done < <(find scripts -maxdepth 1 -type f -name 'test-live-*.sh' -print | sort)
