"""Run the pinned cookbook after checking the Terraform-owned database exists."""
import argparse
import importlib.util
import json
import os
from pathlib import Path

import duckdb
from fetch import fetch


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("config", type=Path)
    parser.add_argument("--fixture", type=Path, help="Local test rows instead of the cookbook's GitHub API source")
    args = parser.parse_args()
    config = json.loads(args.config.read_text())
    allowed = {"DESTINATION_DATABASE", "DATASET_NAME", "TABLE_NAME", "PIPELINE_NAME", "PRIMARY_KEY", "WRITE_DISPOSITION", "GITHUB_REPOS", "RUN_LEDGER_TABLE"}
    if set(config) != allowed or any(not isinstance(v, str) for v in config.values()):
        raise ValueError("Use the pipeline_config output from Terraform")
    # Reject undeployed infrastructure before the upstream's IF NOT EXISTS fallback.
    with duckdb.connect("md:") as con:
        found = con.execute("SELECT count(*) FROM MD_INFORMATION_SCHEMA.DATABASES WHERE name = ?", [config["DESTINATION_DATABASE"]]).fetchone()[0]
        if found != 1:
            raise ValueError("Apply the Terraform database before running ingestion")
    os.environ.update(config)
    spec = importlib.util.spec_from_file_location("cookbook_ingest", fetch())
    recipe = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(recipe)
    if args.fixture:
        rows = json.loads(args.fixture.read_text())
        if not isinstance(rows, list) or any(not isinstance(row, dict) or not row.get("repo") for row in rows):
            raise ValueError("Every fixture row needs a repo merge key")
        recipe.repo_rows = lambda repos: iter(rows)
    recipe.main()

if __name__ == "__main__":
    main()
