"""Cheap, credential-free coverage for examples the Terraform scanner cannot see."""
import ast
import json
from pathlib import Path

root = Path(__file__).resolve().parents[1]
files = [root / 'examples/pulumi/python/__main__.py', *sorted((root / 'examples/cookbook-pipeline').glob('*.py'))]
for path in files:
    ast.parse(path.read_text(), filename=str(path))
pin = json.loads((root / 'examples/cookbook-pipeline/cookbook.json').read_text())
assert len(pin['commit']) == 40 and len(pin['sha256']) == 64
assert pin['repository'] == 'motherduckdb/motherduck-cookbook'
package = json.loads((root / 'examples/customer-facing-analytics/backend/package.json').read_text())
lock = json.loads((root / 'examples/customer-facing-analytics/backend/package-lock.json').read_text())
assert package['dependencies'] == lock['packages']['']['dependencies']
print(f'Companion syntax and dependency metadata checked ({len(files)} Python files, backend lock, cookbook pin).')
