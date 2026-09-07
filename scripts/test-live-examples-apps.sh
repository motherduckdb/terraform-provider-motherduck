#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ -z "${MOTHERDUCK_ADMIN_TOKEN:-}" ]]; then
  echo "MOTHERDUCK_ADMIN_TOKEN is required for fresh service-account example audit" >&2
  exit 1
fi
RUN_ID="${RUN_ID:-tf_deep_app_examples_$(date +%Y%m%d%H%M%S)_$$}"
if [[ ! "${RUN_ID}" =~ ^[A-Za-z0-9_-]+$ ]]; then
  echo "RUN_ID must contain only letters, numbers, underscores, or hyphens" >&2
  exit 1
fi
export RUN_ID
exec python3 "${ROOT_DIR}/scripts/lib/live-examples-apps.py"
