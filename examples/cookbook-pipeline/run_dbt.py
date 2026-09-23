"""Build and test runtime-owned models using a non-secret Terraform output."""
import argparse
import json
import os
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parent
parser = argparse.ArgumentParser()
parser.add_argument("config", type=Path)
parser.add_argument("--target", choices=["dev", "prod"], default="dev")
parser.add_argument("--test-only", action="store_true")
args = parser.parse_args()
config = json.loads(args.config.read_text())
os.environ["DESTINATION_DATABASE"] = config["DESTINATION_DATABASE"]
subprocess.run(["dbt", "test" if args.test_only else "build", "--project-dir", str(ROOT / "dbt"), "--profiles-dir", str(ROOT / "dbt"), "--target", args.target], check=True)
