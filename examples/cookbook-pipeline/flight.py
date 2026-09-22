"""Deploy, run, or delete the pinned ingestion recipe outside Terraform."""
import argparse
import json
import os
import time
import uuid
from pathlib import Path

import duckdb
from fetch import fetch


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("action", choices=["create", "run", "delete"])
    parser.add_argument("config", type=Path)
    parser.add_argument("--state", type=Path, default=Path("flight-id.json"))
    args = parser.parse_args()
    config = json.loads(args.config.read_text())
    with duckdb.connect("md:") as con:
        if args.action == "create":
            if args.state.exists():
                raise ValueError("Flight ID file exists, reuse it or delete its Flight first")
            database = config["DESTINATION_DATABASE"]
            if not con.execute("SELECT count(*) FROM MD_INFORMATION_SCHEMA.DATABASES WHERE name = ?", [database]).fetchone()[0]:
                raise ValueError("Apply the Terraform database first")
            source = fetch().read_text()
            old = 'con.execute(f"CREATE DATABASE IF NOT EXISTS {database}")'
            if source.count(old) != 1:
                raise ValueError("Review the upstream ownership adapter before deployment")
            # The runtime must fail when Terraform infrastructure is absent.
            source = source.replace(old, 'con.execute(f"USE {database}")')
            row = con.execute(
                "SELECT flight_id::VARCHAR FROM MD_CREATE_FLIGHT(name := ?, source_code := ?, requirements_txt := ?, config := MAP(?::VARCHAR[], ?::VARCHAR[]), max_runtime_sec := 600)",
                [database + "_ingest", source, "duckdb==1.5.5\ndlt[motherduck]==1.27.2\nhttpx==0.28.1\n", list(config), list(config.values())],
            ).fetchone()
            args.state.write_text(json.dumps({"flight_id": row[0], "database": database}))
            print("Created on-demand ingestion Flight")
            return
        state = json.loads(args.state.read_text())
        flight_id = str(uuid.UUID(state["flight_id"]))
        if state["database"] != config["DESTINATION_DATABASE"]:
            raise ValueError("Flight state and pipeline config refer to different databases")
        if args.action == "delete":
            con.execute("CALL MD_DELETE_FLIGHT(flight_id := ?::UUID)", [flight_id])
            args.state.unlink()
            print("Deleted ingestion Flight")
            return
        number = con.execute("SELECT run_number FROM MD_RUN_FLIGHT(flight_id := ?::UUID)", [flight_id]).fetchone()[0]
        deadline = time.monotonic() + 660
        while time.monotonic() < deadline:
            row = con.execute("SELECT status FROM MD_LIST_FLIGHT_RUNS(flight_id := ?::UUID) WHERE run_number = ?", [flight_id, number]).fetchone()
            status = row[0].upper().removeprefix("RUN_STATUS_") if row else "PENDING"
            if status == "SUCCEEDED":
                print(f"Flight run {number} succeeded")
                return
            if status in {"FAILED", "CANCELLED", "CANCELED", "ERROR", "ERRORED", "TIMED_OUT", "TIMEOUT"}:
                log_file = args.state.with_suffix(f".run-{number}.log")
                logs = con.execute("SELECT line FROM MD_GET_FLIGHT_LOGS(flight_id := ?::UUID, run_number := ?)", [flight_id, number]).fetchall()
                descriptor = os.open(log_file, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
                with os.fdopen(descriptor, "w") as output:
                    output.write("\n".join(str(line[0]) for line in logs))
                raise RuntimeError(f"Flight run {number} {status}. Inspect {log_file} privately.")
            time.sleep(5)
        con.execute("CALL MD_CANCEL_FLIGHT_RUN(flight_id := ?::UUID, run_number := ?)", [flight_id, number])
        raise TimeoutError("Flight exceeded the polling deadline and cancellation was requested")

if __name__ == "__main__":
    main()
