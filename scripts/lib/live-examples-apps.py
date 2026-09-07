import os, shutil, subprocess, urllib.error, urllib.parse, urllib.request
import platform
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
RUN_ID = os.environ.get('RUN_ID', 'tf_deep_app_example')
BASE = ROOT / 'test-results' / 'audit' / 'deep-app-example' / RUN_ID
BOOT = BASE / 'bootstrap'
APP = BASE / 'app'
FAILED = BASE / 'failed-flight'
for p in (BOOT, APP, FAILED):
    p.mkdir(parents=True, exist_ok=False)
shutil.copytree(ROOT / 'test-fixtures' / 'live-writer-bootstrap', BOOT, dirs_exist_ok=True)
(BOOT / 'terraform.tfvars').write_text(f'run_id = "{RUN_ID}"\n')
(BOOT / 'main.tf').write_text((BOOT / 'main.tf').read_text().replace('= 0.1.0', '= 0.1.1'))
provider_bin = BASE / 'provider'
subprocess.run(['go', 'build', '-o', str(provider_bin), './'], cwd=ROOT, check=True, stdout=subprocess.DEVNULL)
mirror = BASE / 'mirror'
for host in ('registry.terraform.io', 'registry.opentofu.org'):
    goos = 'darwin' if platform.system() == 'Darwin' else 'linux'
    goarch = {'arm64': 'arm64', 'x86_64': 'amd64'}.get(platform.machine(), platform.machine())
    target = mirror / host / 'motherduckdb' / 'motherduck' / '0.1.1' / f'{goos}_{goarch}'
    target.mkdir(parents=True, exist_ok=True)
    os.symlink(provider_bin, target / 'terraform-provider-motherduck_v0.1.1')
terraformrc = f'''provider_installation {{
  filesystem_mirror {{ path = "{mirror}" include = ["registry.terraform.io/motherduckdb/motherduck"] }}
  direct {{ exclude = ["registry.terraform.io/motherduckdb/motherduck"] }}
}}
'''
(BOOT / 'terraformrc').write_text(terraformrc)
def tf(cwd, *args, env=None, capture=False, check=True):
    e = os.environ.copy()
    e['TF_CLI_CONFIG_FILE'] = str(cwd / 'terraformrc')
    if env: e.update(env)
    r = subprocess.run(['terraform', '-chdir=' + str(cwd), *args], env=e, text=True,
                       stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if r.returncode:
        (BASE / 'terraform-error.log').write_text(r.stderr)
    if check and r.returncode:
        raise subprocess.CalledProcessError(r.returncode, r.args, r.stdout, r.stderr)
    return r
tf(BOOT, 'init', '-backend=false', '-input=false')
tf(BOOT, 'apply', '-auto-approve', '-input=false')
username = tf(BOOT, 'output', '-raw', 'writer_username', capture=True).stdout.strip()
token = tf(BOOT, 'output', '-raw', 'writer_token', capture=True).stdout.strip()
example_dive = (ROOT / 'examples/resources/motherduck_dive/resource.tf').read_text()
example_flight_run = (ROOT / 'examples/resources/motherduck_flight_run/resource.tf').read_text()
example_guide = (ROOT / 'examples/resources/motherduck_guide/resource.tf').read_text()
example_data_sources = '\n'.join((ROOT / 'examples/data-sources' / name / 'data-source.tf').read_text()
    .replace('11111111-1111-4111-8111-111111111111',
             '${motherduck_dive.revenue.id}' if 'dive' in name else ('${motherduck_flight.heartbeat_' + RUN_ID + '.id}' if 'flight' in name else '${motherduck_guide.revenue.id}'))
    .replace('123e4567-e89b-42d3-a456-426614174000', '${motherduck_guide.revenue.id}')
    for name in (
        'motherduck_dive', 'motherduck_dive_versions', 'motherduck_dives',
        'motherduck_guide', 'motherduck_guide_versions', 'motherduck_guides',
        'motherduck_flight', 'motherduck_flight_versions', 'motherduck_flight_runs', 'motherduck_flight_logs',
    ))
example_data_sources = example_data_sources.replace(
    'run_number = 1\n}',
    f'run_number = 1\n  depends_on = [motherduck_flight_run.heartbeat_{RUN_ID}]\n}}',
)
example_embed = (ROOT / 'examples/ephemeral-resources/motherduck_dive_embed_session/ephemeral-resource.tf').read_text()
example_dive = example_dive.replace('wikipedia_pageviews', f'tf_example_db_{RUN_ID}').replace('Revenue', f'Revenue {RUN_ID}')
example_guide = example_guide[example_guide.index('resource "motherduck_guide"'):]
example_guide = example_guide.replace('access         = "role"', 'access         = "user"').replace('  role_names     = [motherduck_role.guide_readers.name]\n', '')
example_guide = example_guide.replace('metrics/revenue', f'metrics/revenue/{RUN_ID}').replace('md:analytics', f'md:tf_example_db_{RUN_ID}')
example_flight_run = example_flight_run.replace('heartbeat', f'heartbeat_{RUN_ID}')
example_data_sources = example_data_sources.replace('11111111-1111-4111-8111-111111111111', 'PLACEHOLDER_ID')
example_embed = example_embed.replace('11111111-1111-4111-8111-111111111111', '${motherduck_dive.revenue.id}').replace('analytics_reader', username)
hcl = f'''terraform {{
  required_providers {{ motherduck = {{ source = "motherduckdb/motherduck", version = "= 0.1.1" }} }}
}}
{example_dive}
resource "motherduck_table" "invoices" {{
  database = motherduck_database.wikipedia.name
  schema = "main"
  name = "invoices"
  columns = {{ invoice_id = "INTEGER", amount = "DOUBLE" }}
}}
{example_flight_run}
{example_guide}
{example_data_sources}
{example_embed}
'''
(APP / 'main.tf').write_text(hcl)
(APP / 'terraformrc').write_text(terraformrc)
env = {'MOTHERDUCK_TOKEN': token}
grantees = subprocess.run(
    ['go', 'run', str(ROOT / 'internal/dev/mdexec'), '-scalar', "SELECT count(*)::VARCHAR FROM duckdb_functions() WHERE lower(function_name) = 'md_list_guide_grantees'"],
    env={**os.environ, **env}, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True,
).stdout.strip()
if grantees == '0':
    print('UNAVAILABLE Guide grantees data source and role audience: md_list_guide_grantees is not exposed')
try:
    tf(APP, 'init', '-backend=false', '-input=false', env=env)
    tf(APP, 'apply', '-auto-approve', '-input=false', env=env)
    tf(APP, 'plan', '-detailed-exitcode', '-input=false', env=env)
    print('PASS actual Dive, Guide, Flight, run, catalog data sources, embed data source, and ephemeral embed example')
finally:
    tf(APP, 'destroy', '-auto-approve', '-input=false', env=env, check=False)
(FAILED / 'terraformrc').write_text(terraformrc)
(FAILED / 'main.tf').write_text(f'''terraform {{
  required_providers {{ motherduck = {{ source = "motherduckdb/motherduck", version = "= 0.1.1" }} }}
}}
resource "motherduck_flight" "failed" {{
  name = "tf_failed_{RUN_ID}"
  source_code = "def main():\\n    raise RuntimeError(\\\"intentional deep audit failure\\\")\\n\\nif __name__ == \\\"__main__\\\":\\n    main()"
}}
resource "motherduck_flight_run" "failed" {{
  flight_id = motherduck_flight.failed.id
  wait_for_status = "succeeded"
  poll_interval_seconds = 2
  timeout_seconds = 30
}}
''')
try:
    tf(FAILED, 'init', '-backend=false', '-input=false', env=env)
    failed = tf(FAILED, 'apply', '-auto-approve', '-input=false', env=env, check=False)
    if failed.returncode == 0:
        raise RuntimeError('expected failed Flight execution to fail apply')
    print('failed Flight execution returned expected non-zero apply')
finally:
    tf(FAILED, 'destroy', '-auto-approve', '-input=false', env=env, check=False)
tf(BOOT, 'destroy', '-auto-approve', '-input=false', check=False)
api = os.environ.get('MOTHERDUCK_API_BASE_URL', 'https://api.motherduck.com')
req = urllib.request.Request(api + '/v1/users/' + urllib.parse.quote(username, safe=''), method='GET')
req.add_header('Authorization', 'Bearer ' + os.environ['MOTHERDUCK_ADMIN_TOKEN'])
try:
    urllib.request.urlopen(req)
except urllib.error.HTTPError as exc:
    if exc.code == 404:
        print('cleanup verified 404 for ' + username)
    else:
        raise
else:
    raise RuntimeError('fresh service account still exists')
if grantees == '0':
    raise SystemExit(42)
