"""Build, plan, apply, run dlt/dbt/Flight, then destroy isolated dev/prod roots.

Run with the cookbook-pipeline uv environment and SQL/admin tokens injected.
Logs and recoverable state stay under ignored test-results.
"""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import time
import urllib.request


ROOT = Path(__file__).resolve().parents[1]
RUN = os.environ.get('RUN_ID', time.strftime('%Y%m%d%H%M%S'))
if not RUN.isalnum():
    raise ValueError('RUN_ID must be alphanumeric')
OUT = ROOT / 'test-results' / f'cookbook-pipeline-{RUN}'
OUT.mkdir(mode=0o700)
os.umask(0o077)
base = dict(os.environ)
if not base.get('MOTHERDUCK_TOKEN') or not base.get('MOTHERDUCK_ADMIN_TOKEN'):
    raise ValueError('SQL and admin test credentials are required')
for key in list(base):
    if key.startswith(('TF_CLI_ARGS', 'TF_VAR_')) or key in {'TF_DATA_DIR', 'TF_WORKSPACE', 'TF_REATTACH_PROVIDERS', 'TF_PLUGIN_CACHE_DIR', 'TF_LOG', 'TF_LOG_PATH'}:
        del base[key]
base['CHECKPOINT_DISABLE'] = '1'
base['PATH'] = str(Path(sys.executable).parent) + os.pathsep + base['PATH']
TF = os.environ.get('TERRAFORM_BIN', 'terraform')
sequence = 0


def run(args, cwd=ROOT, env=base, allow=()):
    global sequence
    sequence += 1
    result = subprocess.run([str(x) for x in args], cwd=cwd, env=env, capture_output=True, text=True, timeout=900)
    (OUT / f'{sequence:03d}-{Path(str(args[0])).name}.log').write_text(result.stdout + result.stderr)
    if result.returncode != 0 and result.returncode not in allow:
        raise RuntimeError(f'{args[0]} failed ({result.returncode}), inspect private log {sequence:03d}')
    return result


def tf(folder, env, *args):
    return run([TF, '-chdir=' + str(folder), *args], env=env)


def output(folder, name, env):
    # Do not log token outputs. Terraform already keeps them in protected state.
    data = subprocess.check_output([TF, '-chdir=' + str(folder), 'output', '-json', name], env=env, text=True)
    return json.loads(data)


def query(env, sql):
    # Isolate account credentials and DuckDB's instance cache between processes.
    code = "import duckdb,json,sys; c=duckdb.connect('md:'); print(json.dumps(c.execute(sys.argv[1]).fetchall(),default=str)); c.close()"
    result = run([sys.executable, '-c', code, sql], env=env)
    return [tuple(row) for row in json.loads(result.stdout)]


binary = OUT / 'terraform-provider-motherduck'
run(['go', 'build', '-o', binary, '.'])
platform = subprocess.check_output(['go', 'env', 'GOOS', 'GOARCH'], text=True).split()
mirror = OUT / 'mirror'
package = mirror / 'registry.terraform.io/motherduckdb/motherduck/0.2.10' / '_'.join(platform)
package.mkdir(parents=True)
(package / 'terraform-provider-motherduck_v0.2.10').symlink_to(binary)
cli = OUT / 'terraformrc'
cli.write_text('provider_installation {\n filesystem_mirror {\n path = ' + json.dumps(str(mirror)) + '\n }\n}\n')
base['TF_CLI_CONFIG_FILE'] = str(cli)
results = {}
targets = os.environ.get('PIPELINE_TARGETS', 'dev,prod').split(',')
if not targets or any(t not in {'dev', 'prod'} for t in targets):
    raise ValueError('PIPELINE_TARGETS must contain dev and/or prod')
for target in targets:
    username = f'tf_e08_{RUN}_{target}'
    database = f'tf_e08_{RUN}_{target}'
    bootstrap = OUT / f'bootstrap-{target}'
    bootstrap.mkdir()
    for file in (ROOT / 'examples/blueprints/writer-bootstrap').glob('*.tf'):
        shutil.copy(file, bootstrap)
    (bootstrap / 'provider.tf').write_text('provider "motherduck" {}\n')
    (bootstrap / 'terraform.tfvars.json').write_text(json.dumps({'writer_username': username}))
    runtime = OUT / f'pipeline-{target}'
    shutil.copytree(ROOT / 'examples/cookbook-pipeline', runtime, ignore=shutil.ignore_patterns('.venv', '.runtime', 'target', 'logs', '__pycache__'))
    (runtime / 'terraform.tfvars.json').write_text(json.dumps({'database_name': database}))
    env = dict(base)
    env['DLT_DATA_DIR'] = str(runtime / '.runtime/dlt')
    config = runtime / 'pipeline.json'
    try:
        tf(bootstrap, base, 'init', '-backend=false', '-input=false')
        tf(bootstrap, base, 'plan', '-input=false', '-out=bootstrap.tfplan')
        tf(bootstrap, base, 'apply', '-input=false', 'bootstrap.tfplan')
        env['MOTHERDUCK_TOKEN'] = output(bootstrap, 'writer_token', base)
        tf(runtime, env, 'init', '-backend=false', '-input=false')
        tf(runtime, env, 'plan', '-input=false', '-out=pipeline.tfplan')
        tf(runtime, env, 'apply', '-input=false', 'pipeline.tfplan')
        config.write_text(json.dumps(output(runtime, 'pipeline_config', env)))
        for fixture in ['repos.json', 'repos.json', 'repos-updated.json']:
            run([sys.executable, runtime / 'run_ingest.py', config, '--fixture', runtime / 'fixtures' / fixture], env=env, cwd=runtime)
        assert query(env, f'SELECT repo, stars FROM {database}.raw.github_repo_stats ORDER BY repo') == [('demo/acme', 12), ('demo/globex', 5)]
        failed = run([sys.executable, runtime / 'run_ingest.py', config, '--fixture', runtime / 'fixtures/invalid.json'], env=env, cwd=runtime, allow=(1,))
        assert failed.returncode == 1
        assert query(env, f'SELECT count(*) FROM {database}.raw.github_repo_stats') == [(2,)]
        run([sys.executable, runtime / 'run_dbt.py', config, '--target', target], env=env, cwd=runtime)
        assert query(env, f'SELECT engagement FROM {database}.analytics.repository_metrics ORDER BY repo') == [(15,), (6,)]
        # dbt owns model evolution. Terraform must remain unaware of table changes.
        model = runtime / 'dbt/models/repository_metrics.sql'
        model.write_text(model.read_text().replace('select repo,', "select 'evolved' as model_revision, repo,"))
        run([sys.executable, runtime / 'run_dbt.py', config, '--target', target], env=env, cwd=runtime)
        assert query(env, f'SELECT distinct model_revision FROM {database}.analytics.repository_metrics') == [('evolved',)]
        query(env, f'UPDATE {database}.analytics.repository_metrics SET repo = NULL WHERE repo = \'demo/acme\'')
        failed = run([sys.executable, runtime / 'run_dbt.py', config, '--test-only', '--target', target], env=env, cwd=runtime, allow=(1,))
        assert failed.returncode == 1
        run([sys.executable, runtime / 'run_dbt.py', config, '--target', target], env=env, cwd=runtime)
        tf(runtime, env, 'plan', '-input=false', '-detailed-exitcode')
        if target == 'dev':
            run([sys.executable, runtime / 'flight.py', 'create', config], env=env, cwd=runtime)
            for _ in range(2):
                run([sys.executable, runtime / 'flight.py', 'run', config], env=env, cwd=runtime)
            assert query(env, f"SELECT count(*) FROM {database}.raw.github_repo_stats WHERE repo IN ('duckdb/duckdb','motherduckdb/terraform-provider-motherduck')") == [(2,)]
            tf(runtime, env, 'plan', '-input=false', '-detailed-exitcode')
        results[target] = 'PASS: isolated owner, merge/update/error, dbt build/test/evolution, no-op plan'
    finally:
        cleanup_ok = True
        if (runtime / 'flight-id.json').exists():
            try:
                run([sys.executable, runtime / 'flight.py', 'delete', config], env=env, cwd=runtime)
            except Exception:
                cleanup_ok = False
        if cleanup_ok and (runtime / '.terraform').exists():
            try:
                tf(runtime, env, 'plan', '-destroy', '-input=false', '-out=destroy.tfplan')
                tf(runtime, env, 'apply', '-input=false', 'destroy.tfplan')
                assert query(env, f"SELECT count(*) FROM MD_INFORMATION_SCHEMA.DATABASES WHERE name = '{database}'") == [(0,)]
            except Exception:
                cleanup_ok = False
        if cleanup_ok and (bootstrap / '.terraform').exists():
            tf(bootstrap, base, 'plan', '-destroy', '-input=false', '-out=destroy.tfplan')
            tf(bootstrap, base, 'apply', '-input=false', 'destroy.tfplan')
        if not cleanup_ok:
            raise RuntimeError(f'Cleanup incomplete. Retained state and owner credentials in {OUT}')
        request = urllib.request.Request(f'https://api.motherduck.com/v1/users/{username}/instances', headers={'Authorization': 'Bearer ' + base['MOTHERDUCK_ADMIN_TOKEN']})
        try:
            urllib.request.urlopen(request, timeout=30)
        except urllib.error.HTTPError as error:
            if error.code != 404:
                raise
        else:
            raise AssertionError('Disposable owner remains after destroy')
        print(target + ': teardown completed', flush=True)
(OUT / 'summary.json').write_text(json.dumps(results, indent=2))
print('PASS: cookbook workflows, remote Flight, distinct dev/prod owners, complete cleanup')
