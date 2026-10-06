#!/usr/bin/env bash
# Generates the Pulumi package schema for this checkout through the pinned Any
# Terraform Provider bridge and checks that every resource and data source maps
# to Pulumi. Needs network access for the pinned Pulumi CLI and bridge plugin,
# but no MotherDuck credentials.
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/lib/download-checksum.sh
source "${ROOT_DIR}/scripts/lib/download-checksum.sh"
# shellcheck source=scripts/lib/pulumi-cli.sh
source "${ROOT_DIR}/scripts/lib/pulumi-cli.sh"
RUN_ID="${RUN_ID:-$(date +%Y%m%d%H%M%S)_$$}"
work_dir="${ROOT_DIR}/test-results/pulumi-schema-${RUN_ID}"
if [[ -e "${work_dir}" ]]; then echo "Refusing to reuse ${work_dir}" >&2; exit 1; fi
umask 077
mkdir -p "${work_dir}/bin"
PULUMI_BIN="$(resolve_pulumi_bin "${work_dir}")"
pulumi_isolated_env "${work_dir}"
go build -o "${work_dir}/bin/terraform-provider-motherduck" "${ROOT_DIR}"
(
  cd "${work_dir}"
  "${PULUMI_BIN}" package get-schema "terraform-provider@${PULUMI_BRIDGE_VERSION}" ./bin/terraform-provider-motherduck > schema.json
)
python3 - "${ROOT_DIR}" "${work_dir}/schema.json" <<'PY'
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
schema = json.loads(pathlib.Path(sys.argv[2]).read_text())


def names(directory):
    path = root / "docs" / directory
    return sorted(p.stem for p in path.glob("*.md")) if path.is_dir() else []


def camel(name, upper):
    parts = name.split("_")
    head = parts[0].capitalize() if upper else parts[0]
    return head + "".join(part.capitalize() for part in parts[1:])


errors = []
for resource in names("resources"):
    token = f"motherduck:index/{camel(resource, False)}:{camel(resource, True)}"
    if token not in schema["resources"]:
        errors.append(f"resource motherduck_{resource} has no Pulumi resource {token}")
for data_source in names("data-sources"):
    function = "get" + camel(data_source, True)
    token = f"motherduck:index/{function}:{function}"
    if token not in schema["functions"]:
        errors.append(f"data source motherduck_{data_source} has no Pulumi function {token}")

for key in ("token", "adminToken"):
    if not schema["config"]["variables"].get(key, {}).get("secret"):
        errors.append(f"provider configuration {key} must be a Pulumi secret")

# The bridge does not implement ephemeral resources, so the Pulumi guide must
# name each one and point to its alternative.
guide = (root / "docs" / "guides" / "pulumi.md").read_text()
for ephemeral in names("ephemeral-resources"):
    if f"motherduck_{ephemeral}" not in guide:
        errors.append(f"ephemeral resource motherduck_{ephemeral} is not covered in docs/guides/pulumi.md")

if errors:
    print("\n".join(errors), file=sys.stderr)
    sys.exit(1)
print(f"Pulumi schema maps {len(names('resources'))} resources and {len(names('data-sources'))} data sources")
PY
