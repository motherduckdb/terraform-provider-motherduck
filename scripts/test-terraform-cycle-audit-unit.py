#!/usr/bin/env python3
"""Verify cycle auditing rejects replacement and identity drift without live calls."""
import json
import os
from pathlib import Path
import subprocess
import tempfile

SHIM = Path(__file__).with_name('terraform-cycle-audit.py')
FAKE = r'''#!/usr/bin/env python3
import json,os,sys
from pathlib import Path
p=Path(os.environ['FAKE_COUNTER']);n=int(p.read_text()) if p.exists() else 0
a=[x for x in sys.argv[1:] if not x.startswith('-chdir=')];cmd=a[0]
fault=os.environ.get('FAKE_FAULT','')
if cmd=='apply':
 p.write_text(str(n+1))
elif cmd=='plan':
 if fault=='plan_error': sys.exit(1)
elif cmd=='show' and len(a)>2:
 actions=['create'] if n==0 else (['delete','create'] if fault=='replacement' or (fault=='allowed_replace' and n==1) else ['no-op'])
 print(json.dumps({'resource_changes':[{'address':'motherduck_database.test','mode':'managed','change':{'actions':actions,'before':None if n==0 else {'id':'db'},'after':{'id':'db'}}}]}))
elif cmd=='show':
 changed=n>1 and fault in ('id','allowed_replace')
 resources=[] if n==0 else [{'address':'motherduck_database.test','mode':'managed','values':{'id':'changed' if changed else 'db'}}]
 print(json.dumps({'values':{'root_module':{'resources':resources}}}))
'''

with tempfile.TemporaryDirectory() as directory:
    base = Path(directory)
    fake = base / 'terraform'
    fake.write_text(FAKE)
    fake.chmod(0o700)
    for case in ('success', 'id', 'replacement', 'allowed_replace', 'plan_error', 'zero'):
        root = base / case
        root.mkdir()
        if case == 'allowed_replace':
            (root / 'count').write_text('1')
        env = os.environ | {
            'MD_CYCLE_TERRAFORM': str(fake), 'MD_CYCLE_REPORT_DIR': str(root),
            'FAKE_COUNTER': str(root / 'count'), 'FAKE_FAULT': case,
            'MD_CYCLE_REPEATS': '0' if case == 'zero' else '5',
            'MD_CYCLE_ALLOW_REPLACE': '1' if case == 'allowed_replace' else '0',
        }
        result = subprocess.run([str(SHIM), 'apply', '-auto-approve', '-input=false'],
                                env=env, capture_output=True, text=True)
        expected_success = case in ('success', 'allowed_replace')
        if (result.returncode == 0) != expected_success:
            raise AssertionError(f'{case}: unexpected exit {result.returncode}: {result.stderr}')
        if case in ('plan_error', 'zero'):
            assert not (root / 'count').exists(), 'Invalid audit input must not apply'
        if expected_success:
            records = [json.loads(p.read_text()) for p in root.glob('*.json')]
            assert len(records) == 1 and records[0]['status'] == 'passed'
            assert len(records[0]['cycles']) == 5
            assert int((root / 'count').read_text()) == (7 if case == 'allowed_replace' else 6)
        print('Cycle auditor check passed:', case)
