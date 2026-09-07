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
hcl = f'''terraform {{
  required_providers {{ motherduck = {{ source = "motherduckdb/motherduck", version = "= 0.1.1" }} }}
}}
resource "motherduck_database" "wikipedia" {{ name = "tf_example_db_{RUN_ID}" }}
resource "motherduck_table" "invoices" {{
  database = motherduck_database.wikipedia.name
  schema = "main"
  name = "invoices"
  columns = {{ invoice_id = "INTEGER", amount = "DOUBLE" }}
}}
resource "motherduck_share" "wikipedia" {{
  name = "tf_example_share_{RUN_ID}"
  source_database = motherduck_database.wikipedia.name
  access = "unrestricted"
  visibility = "hidden"
  update_mode = "automatic"
}}
resource "motherduck_dive" "revenue" {{
  title = "Revenue {RUN_ID}"
  description = "Revenue overview"
  api_version = 1
  status = "ready"
  required_resources = [{{ alias = "wikipedia_pageviews", url = motherduck_share.wikipedia.url }}]
  content = "export default function Dive() {{ return <div>Revenue</div>; }}"
}}
resource "motherduck_guide" "revenue" {{
  topic = "metrics/revenue/{RUN_ID}"
  title = "Revenue metrics"
  description = "Canonical revenue definitions"
  content = "# Revenue metrics\\n\\nRevenue is calculated from finalized invoices."
  references = [{{ type = "catalog", url = "md:tf_example_db_{RUN_ID}", schema = "main", table = "invoices", description = "Authoritative invoice source" }}]
}}
resource "motherduck_flight" "heartbeat" {{
  name = "tf_heartbeat_{RUN_ID}"
  max_runtime_sec = 900
  config = {{ mode = "default" }}
  source_code = "def main():\\n    print(\\\"hello\\\")\\n\\nif __name__ == \\\"__main__\\\":\\n    main()"
}}
resource "motherduck_flight_run" "heartbeat" {{
  flight_id = motherduck_flight.heartbeat.id
  config = {{ mode = "manual" }}
  wait_for_status = "succeeded"
  poll_interval_seconds = 5
  timeout_seconds = 120
}}
data "motherduck_dive" "revenue" {{ dive_id = motherduck_dive.revenue.id }}
data "motherduck_dive_versions" "revenue" {{ dive_id = motherduck_dive.revenue.id }}
data "motherduck_dives" "recent" {{ limit = 10 }}
data "motherduck_guide" "revenue" {{ guide_id = motherduck_guide.revenue.id }}
data "motherduck_guide_versions" "revenue" {{ guide_id = motherduck_guide.revenue.id }}
data "motherduck_guides" "recent" {{ limit = 10 }}
data "motherduck_flight" "heartbeat" {{ flight_id = motherduck_flight.heartbeat.id }}
data "motherduck_flight_versions" "heartbeat" {{ flight_id = motherduck_flight.heartbeat.id }}
data "motherduck_flight_runs" "heartbeat" {{ flight_id = motherduck_flight.heartbeat.id }}
data "motherduck_flight_logs" "heartbeat" {{
  flight_id = motherduck_flight.heartbeat.id
  run_number = motherduck_flight_run.heartbeat.run_number
}}
data "motherduck_dive_embed_session" "legacy" {{
  dive_id = motherduck_dive.revenue.id
  username = "{username}"
}}
ephemeral "motherduck_dive_embed_session" "current" {{
  dive_id = motherduck_dive.revenue.id
  username = "{username}"
  session_hint = "deep-example"
}}
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
