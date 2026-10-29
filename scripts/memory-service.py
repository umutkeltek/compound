#!/usr/bin/env python3
"""Configure and run approved memory refresh through Compound's execution rail.

Setup is itself run inside an operator-authorized WorkEnvelope. Every hook-triggered pass
reserves a fresh action/policy through work advance --execute; it never edits the
ledger, promotes source statements, or revives retired ingestion jobs.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys

ACTOR = "automation:memory-refresh"


def run(binary, args, capture=False):
    result = subprocess.run([str(binary), *args], text=True,
                            stdout=subprocess.PIPE if capture else None, check=False)
    if result.returncode:
        raise RuntimeError("Compound refused or failed: " + " ".join(args[:2]))
    return json.loads(result.stdout) if capture else None


def work_id(home):
    return "work_memory_refresh_" + hashlib.sha256(str(home).encode()).hexdigest()[:16]


def tick(binary, home, workspace):
    wid = work_id(home)
    record = run(binary, ["work", "show", wid, "--json"], capture=True)
    state = record["state"]
    if state not in ("claimed", "verifying"):
        raise RuntimeError("maintenance work is " + state + "; no automatic takeover")
    args = ["work", "advance", wid, "--to", "running", "--by", ACTOR,
            "--execute", "--harness", "os-harness-entrypoints",
            "--workspace", str(workspace), "--timeout", "5m"]
    if state == "verifying":
        args += ["--resume-verifying"]
    run(binary, args + ["--", str(binary), "corpus", "refresh"])


def setup(binary, home, workspace, config):
    # This operation is called inside the setup work's admitted subprocess.
    run(binary, ["corpus", "configure", "--file", str(config)])
    wid = work_id(home)
    probe = subprocess.run([str(binary), "work", "show", wid, "--json"],
                           capture_output=True, text=True)
    if probe.returncode:
        run(binary, ["work", "create", "--work-id", wid, "--owner", ACTOR,
                     "--by", ACTOR, "--state", "ready",
                     "--predicate", "Continuously capture approved memory sources and report indexed coverage without promoting source evidence"])
        run(binary, ["work", "claim", wid, "--by", ACTOR])
        # Establish a resumable lifecycle before the first potentially failing
        # capture. This no-op is not capture/index success evidence.
        run(binary, ["work", "advance", wid, "--to", "running", "--by", ACTOR,
                     "--execute", "--harness", "os-harness-entrypoints",
                     "--workspace", str(workspace), "--timeout", "1m",
                     "--", "/usr/bin/true"])
    else:
        record = json.loads(probe.stdout)
        if record["owner"] != ACTOR or record["state"] not in ("claimed", "verifying"):
            raise RuntimeError("existing maintenance work is not available")
    # Live hooks perform the first capture through the same native resume path.
    print(json.dumps({"status": "configured", "work_id": wid,
                      "trigger": "live PostToolUse and SessionEnd hooks",
                      "interval_seconds": 300}))


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("operation", choices=("setup", "tick"))
    p.add_argument("--binary", type=Path, required=True)
    p.add_argument("--home", type=Path, required=True)
    p.add_argument("--workspace", type=Path, required=True)
    p.add_argument("--config", type=Path)
    a = p.parse_args()
    for value in (a.binary, a.home, a.workspace):
        if not value.is_absolute() or not value.exists():
            p.error("binary, home and workspace must be existing absolute paths")
    os.environ["COMPOUND_HOME"] = str(a.home)
    try:
        if a.operation == "tick":
            tick(a.binary, a.home, a.workspace)
        else:
            if a.config is None:
                p.error("setup requires --config with approved sources")
            setup(a.binary, a.home, a.workspace, a.config)
    except (RuntimeError, OSError, ValueError, subprocess.CalledProcessError) as e:
        print("memory service: " + str(e), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
