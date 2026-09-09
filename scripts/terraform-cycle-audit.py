#!/usr/bin/env python3
"""Terraform shim for live apply/plan repetition and identity auditing."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import uuid

os.umask(0o077)
CLI = os.environ['MD_CYCLE_TERRAFORM']
BASE = Path(os.environ['MD_CYCLE_REPORT_DIR'])
BASE.mkdir(parents=True, exist_ok=True)
REPEATS = int(os.environ.get('MD_CYCLE_REPEATS', '5'))
if REPEATS < 1:
    raise SystemExit('MD_CYCLE_REPEATS must be a positive integer')
args = sys.argv[1:]
position = next((i for i, arg in enumerate(args) if not arg.startswith('-')), None)
if position is None or args[position] != 'apply':
    os.execv(CLI, [CLI, *args])
global_args = args[:position]
apply_args = args[position + 1:]
if any(not arg.startswith('-') for arg in apply_args):
    raise SystemExit('Cycle audit requires apply options in -key=value form, without a saved plan')
plan_args = [arg for arg in apply_args if arg != '-auto-approve']
root = next((arg.split('=', 1)[1] for arg in global_args if arg.startswith('-chdir=')), os.getcwd())
run_id = uuid.uuid4().hex
record = {'root': root, 'invocation': run_id, 'cycles': [], 'status': 'running'}


def call(command, *options, capture=True):
    result = subprocess.run([CLI, *global_args, command, *options], capture_output=capture, text=True)
    if capture and result.returncode not in (0, 2):
        (BASE / (run_id + '-error.log')).write_text(result.stdout + result.stderr)
    return result


def resources(document):
    result = {}
    def walk(module):
        for resource in module.get('resources', []):
            if resource.get('mode') == 'managed':
                values = resource.get('values', {})
                result[resource['address']] = {key: values[key] for key in ('id', 'uuid', 'created_ts', 'created_at', 'run_number', 'current_version', 'version') if values.get(key) is not None}
        for child in module.get('child_modules', []):
            walk(child)
    walk(document.get('values', {}).get('root_module', {}))
    return result


def state():
    output = call('show', '-json')
    if output.returncode:
        raise RuntimeError('Unable to inspect state')
    return resources(json.loads(output.stdout))


def plan(directory):
    filename = str(Path(directory) / 'audit.tfplan')
    result = call('plan', *plan_args, '-detailed-exitcode', '-out=' + filename)
    if result.returncode not in (0, 2):
        return None, result.returncode
    shown = call('show', '-json', filename)
    if shown.returncode:
        raise RuntimeError('Unable to inspect saved plan')
    data = json.loads(shown.stdout)
    return {r['address']: r['change'] for r in data.get('resource_changes', []) if r.get('mode') == 'managed'}, result.returncode


def unchanged(before, after, changes):
    for address in before.keys() & after.keys():
        change = changes.get(address, {})
        if 'delete' in change.get('actions', []):
            continue
        # Snapshot rename is an explicit identity-affecting operation. Record it
        # separately rather than asserting an undocumented backend invariant.
        if address.startswith('motherduck_snapshot.') and (change.get('before') or {}).get('name') != (change.get('after') or {}).get('name'):
            record.setdefault('planned_snapshot_renames', []).append(address)
            continue
        if {k: v for k, v in before[address].items() if k not in ('current_version', 'version')} != {k: v for k, v in after[address].items() if k not in ('current_version', 'version')}:
            raise RuntimeError('Unplanned identity or creation-metadata change: ' + address)


try:
    before = state()
    record['before_ids'] = before
    with tempfile.TemporaryDirectory(prefix='tf-cycle-') as temporary:
        changes, status = plan(temporary)
        if changes is None:
            raise RuntimeError('Pre-apply plan failed. No apply was attempted')
        replacements = [a for a, c in changes.items() if 'delete' in c['actions'] and 'create' in c['actions']]
        if replacements and os.environ.get('MD_CYCLE_ALLOW_REPLACE') != '1':
            raise RuntimeError('Unexpected replacement in apply plan: ' + ', '.join(replacements))
        record['planned_actions'] = {a: c['actions'] for a, c in changes.items()}
        result = subprocess.run([CLI, *args])
        if result.returncode:
            record['status'] = 'apply_failed'
            sys.exit(result.returncode)
        after = state()
        unchanged(before, after, changes)
        record['initial_ids'] = after
        if after and '-refresh-only' not in apply_args:
            for number in range(1, REPEATS + 1):
                next_changes, status = plan(temporary)
                if next_changes is None:
                    raise RuntimeError('Repeated plan failed')
                unexpected = {a: c['actions'] for a, c in next_changes.items() if c['actions'] != ['no-op']}
                if unexpected:
                    raise RuntimeError('Repeated plan proposed managed changes: ' + json.dumps(unexpected))
                applied = call('apply', *apply_args)
                if applied.returncode:
                    raise RuntimeError('Repeated apply failed')
                current = state()
                if current != after:
                    raise RuntimeError('Repeated apply changed resource identities or creation metadata')
                record['cycles'].append({'cycle': number, 'managed_actions': 'no-op', 'ids_unchanged': True})
        record['status'] = 'passed'
except Exception as error:
    record['status'] = 'failed'
    record['error'] = str(error)
    print(str(error), file=sys.stderr)
    sys.exit(1)
finally:
    (BASE / (run_id + '.json')).write_text(json.dumps(record, indent=2) + '\n')
