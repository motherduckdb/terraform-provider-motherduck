#!/usr/bin/env bash

for_each_version() {
  local callback="$1"
  shift

  local version
  for version in "$@"; do
    "${callback}" "${version}"
  done
}

dispatch_cli_version_families() {
  local terraform_callback="$1"
  local opentofu_callback="$2"

  # The guarded expansions are required by Bash 3.2 when set -u is enabled.
  # shellcheck disable=SC2154
  for_each_version "${terraform_callback}" "${TF_VERSION_LIST[@]+"${TF_VERSION_LIST[@]}"}"
  # shellcheck disable=SC2154
  for_each_version "${opentofu_callback}" "${TOFU_VERSION_LIST[@]+"${TOFU_VERSION_LIST[@]}"}"
}
