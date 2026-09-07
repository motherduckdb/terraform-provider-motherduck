"""Deploy the checked-in app examples under a disposable writer identity."""

import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request


ROOT = Path(__file__).resolve().parents[2]
BASE = Path(os.environ["MD_EXAMPLE_DIR"])
SUFFIX = os.environ["RUN_ID"].replace("-", "_")
USERNAME = "tf_app_examples_" + SUFFIX
CLI = os.environ.get("TERRAFORM_BIN", "terraform")
VERSION = os.environ["PROVIDER_VERSION"]
BOOT = BASE / "bootstrap"
APP = BASE / "apps"
FAILED = BASE / "failed-flight"
REPORT = {"account": USERNAME, "examples": {}, "checks": {}, "cleanup": {}}
SECRETS = [os.environ.get("MOTHERDUCK_TOKEN", ""), os.environ["MOTHERDUCK_ADMIN_TOKEN"]]
counter = 0


def environment(token=None):
    env = {k: v for k, v in os.environ.items()
           if not k.startswith(("TF_VAR_", "TF_CLI_ARGS"))
           and k not in ("TF_DATA_DIR", "TF_WORKSPACE", "TF_REATTACH_PROVIDERS", "TF_PLUGIN_CACHE_DIR",
                         "TF_LOG", "TF_LOG_PATH", "TF_LOG_PROVIDER", "TF_LOG_CORE")}
    env.update(TF_IN_AUTOMATION="1", CHECKPOINT_DISABLE="1")
    if token is not None:
        env["MOTHERDUCK_TOKEN"] = token
    return env


def redact(text):
    for secret in SECRETS:
        if secret:
            text = text.replace(secret, "<redacted>")
    return text


def tf(directory, *args, token=None, allowed=(0,), private_output=False):
    global counter
    counter += 1
    result = subprocess.run([CLI, "-chdir=" + str(directory), *args],
                            env=environment(token), capture_output=True, text=True, timeout=1800)
    log = BASE / f"{counter:03d}-{directory.name}-{args[0]}.log"
    if not private_output:
        log.write_text(redact(result.stdout + result.stderr))
    if result.returncode not in allowed:
        raise RuntimeError(f"{directory.name}: {args[0]} exited {result.returncode}; see {log}")
    return result


def sql(query, token):
    result = subprocess.run(["go", "run", "./internal/dev/mdexec", "-scalar", query],
                            cwd=ROOT, env=environment(token), capture_output=True, text=True, timeout=180)
    if result.returncode:
        (BASE / "sql-error.log").write_text(redact(result.stderr))
        raise RuntimeError("Independent SQL check failed; see sql-error.log")
    return result.stdout.strip()


def sample(path):
    REPORT["examples"][path] = "staged"
    return (ROOT / path).read_text()


def config(text):
    return f'''terraform {{
  required_providers {{
    motherduck = {{ source = "motherduckdb/motherduck", version = "= {VERSION}" }}
  }}
}}
provider "motherduck" {{}}
''' + text


def save_report():
    (BASE / "coverage.json").write_text(json.dumps(REPORT, indent=2) + "\n")


def check(label):
    REPORT["checks"][label] = "PASS"
    print("PASS " + label, flush=True)
    save_report()


def prepare_apps():
    pieces = [sample("examples/resources/motherduck_dive/resource.tf"),
              sample("examples/resources/motherduck_guide/resource.tf"),
              sample("examples/resources/motherduck_flight_run/resource.tf"),
              sample("examples/resources/motherduck_flight/resource.tf").replace("heartbeat", "standalone")]
    source_types = ("dive", "dive_versions", "dives", "guide", "guide_versions", "guides",
                    "flight", "flight_versions", "flight_runs", "flight_logs", "flights", "dive_embed_session")
    for kind in source_types:
        text = sample(f"examples/data-sources/motherduck_{kind}/data-source.tf")
        family = "dive" if "dive" in kind else "guide" if "guide" in kind else "flight"
        resource = "motherduck_flight.heartbeat" if family == "flight" else f"motherduck_{family}.revenue"
        text = re.sub(r'"(?:11111111-1111-4111-8111-111111111111|123e4567-e89b-42d3-a456-426614174000)"',
                      resource + ".id", text)
        text = re.sub(r"run_number\s*=\s*1", "run_number = motherduck_flight_run.heartbeat.run_number", text)
        pieces.append(text.replace('"analytics_reader"', json.dumps(USERNAME)))
    text = sample("examples/ephemeral-resources/motherduck_dive_embed_session/ephemeral-resource.tf")
    pieces.append(text.replace('"11111111-1111-4111-8111-111111111111"', "motherduck_dive.revenue.id")
                  .replace('"analytics_reader"', json.dumps(USERNAME)))
    text = "\n".join(pieces)
    for name in ("analytics", "wikipedia_pageviews", "heartbeat", "standalone"):
        text = re.sub(r'(\bname\s*=\s*)"' + name + '"', r'\g<1>"tf_app_' + SUFFIX + "_" + name + '"', text)
    text = text.replace('"metrics/revenue"', json.dumps("metrics/revenue/" + SUFFIX))
    text += '''
output "audit_ids" {
  value = {
    dive = motherduck_dive.revenue.id
    guide = motherduck_guide.revenue.id
    flight = motherduck_flight.heartbeat.id
    guide_version = motherduck_guide.revenue.current_version
    run_number = motherduck_flight_run.heartbeat.run_number
  }
}
'''
    (APP / "main.tf").write_text(config(text))
    return text


def ids(token):
    return json.loads(tf(APP, "output", "-json", "audit_ids", token=token, private_output=True).stdout)


def plan(token):
    return tf(APP, "plan", "-input=false", "-detailed-exitcode", token=token)


def verify_catalogs(token, expected):
    state = json.loads(tf(APP, "show", "-json", token=token, private_output=True).stdout)
    resources = state["values"]["root_module"]["resources"]
    for item in resources:
        if item["mode"] != "data":
            continue
        values = item["values"]
        if item["type"] == "motherduck_dive_embed_session":
            session = values.get("session")
            if not session or item.get("sensitive_values", {}).get("session") is not True:
                raise RuntimeError("Embed session must be nonempty and marked sensitive")
            SECRETS.append(session)
            continue
        rows = json.loads(values["rows_json"])
        if not rows:
            raise RuntimeError(f"{item['address']} returned no rows for the deployed example")
        family = item["type"].removeprefix("motherduck_")
        if family in ("dive", "guide", "flight") and expected[family] not in json.dumps(rows):
            raise RuntimeError(f"{item['address']} did not return its requested object")
    if any(item["mode"] == "ephemeral" for item in resources):
        raise RuntimeError("Ephemeral embed session unexpectedly persisted in state")
    check("catalogs return fixture objects; embed credentials retain secret state semantics")


def run_examples(token):
    text = prepare_apps()
    tf(APP, "init", "-backend=false", "-input=false", token=token)
    tf(APP, "validate", token=token)
    tf(APP, "apply", "-auto-approve", "-input=false", token=token)
    initial = ids(token)
    tf(APP, "refresh", "-input=false", token=token)
    plan(token)
    verify_catalogs(token, initial)
    for path in REPORT["examples"]:
        REPORT["examples"][path] = "PASS"
    check("actual app examples: create, catalog reads, refresh, no-op")

    updated = text.replace("<div>Revenue</div>", "<div>Revenue updated</div>")
    updated = updated.replace("Revenue is calculated from finalized invoices.",
                              "Revenue is calculated from finalized invoices. Refunds reduce revenue.")
    updated = updated.replace('print("hello")', 'print("hello updated")')
    if updated == text:
        raise RuntimeError("Example content changed; update probes need review")
    (APP / "main.tf").write_text(config(updated))
    tf(APP, "apply", "-auto-approve", "-input=false", token=token)
    after = ids(token)
    for kind in ("dive", "guide", "flight"):
        if initial[kind] != after[kind]:
            raise RuntimeError(f"{kind} content update replaced its identity")
    if after["guide_version"] <= initial["guide_version"] or after["run_number"] != initial["run_number"]:
        raise RuntimeError("Expected a new Guide version without executing the Flight again")
    plan(token)
    check("content updates preserve identity and do not replay Flight runs")

    for kind in ("guide", "flight", "dive"):
        address = "motherduck_flight.heartbeat" if kind == "flight" else f"motherduck_{kind}.revenue"
        tf(APP, "state", "rm", address, token=token)
        tf(APP, "import", "-input=false", address, str(after[kind]), token=token)
        if kind == "dive":
            # Public getters cannot recover api_version; one configured content update restores it.
            tf(APP, "apply", "-auto-approve", "-input=false", token=token)
        plan(token)
        check(kind + " import and no-op")

    available = sql("SELECT count(*)::VARCHAR FROM duckdb_functions() WHERE lower(function_name) = 'md_list_guide_grantees'", token)
    path = "examples/data-sources/motherduck_guide_grantees/data-source.tf"
    if available == "0":
        REPORT["examples"][path] = "UNAVAILABLE: md_list_guide_grantees is not exposed"
        print("UNAVAILABLE Guide grantees: md_list_guide_grantees is not exposed", flush=True)
    else:
        grantees = sample(path).replace('"123e4567-e89b-42d3-a456-426614174000"', "motherduck_guide.revenue.id")
        (APP / "grantees.tf").write_text(grantees)
        tf(APP, "apply", "-auto-approve", "-input=false", token=token)
        REPORT["examples"][path] = "PASS"
    save_report()

    failing = sample("examples/resources/motherduck_flight_run/resource.tf")
    failing = failing.replace('name = "heartbeat"', f'name = "tf_failed_{SUFFIX}"')
    failing = failing.replace('print("hello")', 'raise RuntimeError("expected example audit failure")')
    (FAILED / "main.tf").write_text(config(failing))
    tf(FAILED, "init", "-backend=false", "-input=false", token=token)
    result = tf(FAILED, "apply", "-auto-approve", "-input=false", token=token, allowed=(0, 1))
    diagnostic = (result.stdout + result.stderr).lower()
    if result.returncode != 1 or "motherduck flight run failed" not in diagnostic:
        raise RuntimeError("Expected failed Flight execution diagnostic, not an unrelated failure")
    REPORT["examples"]["examples/resources/motherduck_flight_run/resource.tf"] = "PASS"
    check("failed Flight execution produces an actionable apply error")


def verify_account(expected_status):
    url = os.environ.get("MOTHERDUCK_API_BASE_URL", "https://api.motherduck.com").rstrip("/")
    request = urllib.request.Request(url + "/v1/users/" + urllib.parse.quote(USERNAME, safe="") + "/instances",
                                     headers={"Authorization": "Bearer " + os.environ["MOTHERDUCK_ADMIN_TOKEN"]})
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            status = response.status
    except urllib.error.HTTPError as error:
        status = error.code
    if status != expected_status:
        raise RuntimeError(f"Account readback returned HTTP {status}, expected {expected_status}")
    if expected_status == 404:
        REPORT["cleanup"][USERNAME] = "HTTP 404 from /instances"
        check("disposable account cleanup")
    else:
        check("disposable account exists: HTTP 200 from /instances")


def main():
    os.umask(0o077)
    for directory in (BOOT, APP, FAILED):
        directory.mkdir()
    for path in (ROOT / "examples/blueprints/writer-bootstrap").glob("*.tf"):
        shutil.copy2(path, BOOT / path.name)
    (BOOT / "terraform.tfvars.json").write_text(json.dumps({"writer_username": USERNAME, "writer_token_ttl_seconds": 3600}))
    token = None
    error = None
    try:
        tf(BOOT, "init", "-backend=false", "-input=false")
        tf(BOOT, "apply", "-auto-approve", "-input=false")
        verify_account(200)
        token = tf(BOOT, "output", "-raw", "writer_token", private_output=True).stdout.strip()
        if not token:
            raise RuntimeError("Bootstrap returned an empty writer token")
        SECRETS.append(token)
        identity = sql("SELECT md_user()", token)
        if identity != USERNAME:
            raise RuntimeError("SQL identity does not match the disposable writer")
        check("fresh writer identity")
        run_examples(token)
    except Exception as failure:
        error = str(failure)
    finally:
        cleanup_errors = []
        for directory in (FAILED, APP):
            if token and (directory / ".terraform").exists():
                try:
                    tf(directory, "destroy", "-auto-approve", "-input=false", token=token)
                    REPORT["cleanup"][directory.name] = "PASS"
                except Exception as failure:
                    cleanup_errors.append(str(failure))
        if not cleanup_errors and (BOOT / ".terraform").exists():
            try:
                tf(BOOT, "destroy", "-auto-approve", "-input=false")
                verify_account(404)
            except Exception as failure:
                cleanup_errors.append(str(failure))
        if cleanup_errors:
            REPORT["cleanup"]["errors"] = cleanup_errors
            error = (error or "") + "; cleanup failed, retain bootstrap state: " + "; ".join(cleanup_errors)
        if error:
            REPORT["error"] = redact(error)
        save_report()
    if error:
        print(redact(error), file=sys.stderr)
        return 1
    return 42 if any(value.startswith("UNAVAILABLE") for value in REPORT["examples"].values()) else 0


if __name__ == "__main__":
    sys.exit(main())
