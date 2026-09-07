#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="$(mktemp -d)"
trap 'rm -rf "${fixture}"' EXIT
mkdir -p "${fixture}/scripts/lib" "${fixture}/docs"
printf '#!/bin/bash\ntrue\n' > "${fixture}/scripts/a-valid.sh"
printf '#!/bin/bash\nif then\n' > "${fixture}/scripts/z-invalid.sh"

# bash -n accepts only one script. The gate must inspect the later file too,
# before executing any of the behavioral test scripts.
if make -s -C "${fixture}" -f "${ROOT_DIR}/Makefile" test-scripts > "${fixture}/syntax.log" 2>&1; then
  echo 'Syntax gate accepted invalid second script' >&2; exit 1
fi
grep -q 'z-invalid.sh.*syntax error' "${fixture}/syntax.log"

# A failed generator that leaves docs unchanged must still fail the gate.
if make -s -C "${fixture}" -f "${ROOT_DIR}/Makefile" docs-check MAKE=false > "${fixture}/docs.log" 2>&1; then
  echo 'Documentation gate ignored generator failure' >&2; exit 1
fi
echo 'Gate failure-path tests passed'
