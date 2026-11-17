#!/usr/bin/env python3
"""Configure and run approved memory refresh through Compound's execution rail.

Setup is itself run inside an operator-authorized WorkEnvelope. Every hook-triggered pass
reserves a fresh action/policy through work advance --execute; it never edits the
ledger, promotes source statements, or revives retired ingestion jobs.
Explicit tick --start may claim only this service's existing READY work.
Tick --recovery-only runs the same admitted recovery without corpus refresh;
HELD work still requires operator release outside this helper.
"""
import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys

ACTOR = "automation:memory-refresh"

import datetime
import re
import time
from urllib.parse import urlparse

RECOVERY_MARKER = "memory-backend-recovery:"


def backend_health(binary):
    result = subprocess.run([str(binary), "corpus", "backend-health"], text=True,
                            capture_output=True, timeout=50, check=False)
    report = json.loads(result.stdout)
    if report.get("status") == "ready" and result.returncode != 0:
        raise RuntimeError("backend health command failed")
    if not isinstance(report.get("backends"), list):
        raise RuntimeError("backend health report is invalid")
    return report


def recovery_budget(binary, home, now):
    # Reuse the native attempt transcript: no parallel retry ledger or new
    # COMPOUND_HOME family. The marker is flushed BEFORE kickstart, so a crash
    # consumes a retry rather than creating an endless restart loop.
    record = run(binary, ["work", "show", work_id(home), "--json"], capture=True)
    ids = record.get("run_attempt_ids", [])
    recent = []
    for rid in ids[-512:]:
        if not re.fullmatch(r"ratt_[A-Za-z0-9_]+", rid):
            raise RuntimeError("invalid recovery attempt identity")
        attempt = json.loads((home / "run_attempts" / (rid + ".json")).read_text())
        created = datetime.datetime.fromisoformat(attempt["created_at"].replace("Z", "+00:00")).timestamp()
        if created > now + 60:
            raise RuntimeError("recovery history clock is ahead")
        if now - created > 86400 + 480:
            continue
        if attempt.get("origin") != "harness_execution":
            continue
        path = home / "run_attempts" / (rid + ".transcript")
        if not path.exists() and attempt.get("status") in ("pending", "running"):
            continue
        with path.open() as stream:
            for line in stream.read(65536).splitlines():
                if line.startswith("stdout: " + RECOVERY_MARKER):
                    stamp = float(line.removeprefix("stdout: " + RECOVERY_MARKER))
                    if stamp < created - 1 or stamp > created + 480 or stamp > now + 60:
                        raise RuntimeError("recovery marker clock is invalid")
                    if now - stamp <= 86400:
                        recent.append(stamp)
                    break
    if len(ids) > 512:
        oldest = json.loads((home / "run_attempts" / (ids[-512] + ".json")).read_text())
        age = now - datetime.datetime.fromisoformat(oldest["created_at"].replace("Z", "+00:00")).timestamp()
        if age < 86400:
            raise RuntimeError("recovery history exceeds bounded inspection")
    if len(recent) >= 3:
        raise RuntimeError("backend_unavailable: daily recovery limit reached")
    if recent and now - max(recent) < 1800:
        raise RuntimeError("backend_unavailable: recovery cooldown (30 minutes)")


def refresh_active(binary=None):
    probe = subprocess.run(["/bin/ps", "-axo", "args="], text=True,
                           capture_output=True, timeout=5, check=True)
    exact = re.escape(str(binary)) + "|" if binary is not None else ""
    executable = re.compile(r"^\s*(?:" + exact + r"(?:\S*/)?compound)\s+")
    operation = re.compile(r"^(?:--home(?:=|\s+).+?\s+)?corpus\s+(?:refresh|index)(?:\s|$)")
    for line in probe.stdout.splitlines():
        match = executable.match(line)
        if match and operation.match(line[match.end():]):
            return True
    return False


def recover_backend(binary, home):
    report = backend_health(binary)
    print(json.dumps({"memory_backend": report}), flush=True)
    if report["status"] == "ready":
        return
    bad = [c for c in report["backends"] if c.get("status") != "ready"]
    if not bad or any(c.get("status") not in ("process_unavailable", "process_unhealthy") for c in bad):
        raise RuntimeError("backend_unavailable: model missing or incompatible; manual repair required")
    # The installed owner controls only its canonical local listener. A custom
    # port, proxy, remote endpoint or another service must never be restarted.
    for check in report["backends"]:
        endpoint = urlparse(check["base_url"])
        if (check.get("recovery_eligible") is not True or endpoint.scheme != "http"
                or endpoint.username is not None or endpoint.password is not None
                or endpoint.query or endpoint.fragment
                or endpoint.hostname not in ("localhost", "127.0.0.1", "::1") or endpoint.port != 11434):
            raise RuntimeError("backend_unavailable: endpoint has no approved service owner")
    label = "gui/" + str(os.getuid()) + "/sh.brew.ollama"
    owner = subprocess.run(["/bin/launchctl", "print", label], capture_output=True,
                           text=True, timeout=5, check=False)
    if owner.returncode != 0 or not re.search(r"program = /[^\n]+/ollama\s", owner.stdout):
        raise RuntimeError("process_unavailable: existing Ollama service owner unavailable")
    classification = "process_unhealthy" if "state = running" in owner.stdout else "process_unavailable"
    print(json.dumps({"memory_backend_status": classification}), flush=True)
    if refresh_active(binary):
        raise RuntimeError("backend_unavailable: active memory refresh/index; recovery deferred")
    now = time.time()
    recovery_budget(binary, home, now)
    require_admitted_refresh(binary, home)
    print(RECOVERY_MARKER + str(int(now)), flush=True)
    restarted = subprocess.run(["/bin/launchctl", "kickstart", "-k", label],
                               timeout=15, check=False)
    if restarted.returncode:
        raise RuntimeError("backend_unavailable: existing owner restart failed")
    # Exactly three readiness checks after one restart. No pull, install, model
    # replacement, daemon creation, or second kill on the failure path.
    for delay in (1, 2, 4):
        time.sleep(delay)
        recovered = backend_health(binary)
        if recovered["status"] == "ready":
            print(json.dumps({"memory_backend_status": "recovered",
                              "backends": recovered["backends"]}), flush=True)
            return
    raise RuntimeError("backend_unavailable: recovery exhausted")



def require_admitted_refresh(binary, home):
    rid = os.environ.get("COMPOUND_RUN_ATTEMPT_ID", "")
    if not re.fullmatch(r"ratt_[A-Za-z0-9_]+", rid):
        raise RuntimeError("refresh requires governed execution")
    attempt = json.loads((home / "run_attempts" / (rid + ".json")).read_text())
    record = maintenance_work(binary, home)
    actions = record.get("run_ids", [])
    if (record.get("state") != "running" or record.get("owner") != ACTOR
            or attempt.get("work_id") != record.get("id")
            or attempt.get("origin") != "harness_execution"
            or attempt.get("status") != "running"
            or not actions or attempt.get("action_intent_id") != actions[-1]
            or not record.get("policy_decision_id")):
        raise RuntimeError("refresh execution reservation changed")


def recover(binary, home):
    require_admitted_refresh(binary, home)
    recover_backend(binary, home)


def refresh(binary, home):
    require_admitted_refresh(binary, home)
    recover_backend(binary, home)
    result = subprocess.run([str(binary), "corpus", "refresh"], check=False)
    if result.returncode:
        print(json.dumps({"memory_backend_status": "indexing_degraded",
                          "detail": "backend ready; corpus refresh failed"}), flush=True)
        raise RuntimeError("indexing_degraded: corpus refresh failed")



def run(binary, args, capture=False, timeout=None, lock_fd=None):
    if timeout is None:
        result = subprocess.run([str(binary), *args], text=True,
                                stdout=subprocess.PIPE if capture else None, check=False)
    else:
        # SIGTERM lets the native execution owner finalize its attempt and stop
        # its own children. subprocess.run(timeout=...) would send SIGKILL.
        with subprocess.Popen([str(binary), *args], text=True,
                              stdout=subprocess.PIPE if capture else None,
                              pass_fds=() if lock_fd is None else (lock_fd,)) as process:
            try:
                stdout, _ = process.communicate(timeout=timeout)
            except subprocess.TimeoutExpired:
                process.terminate()
                try:
                    process.communicate(timeout=30)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.communicate()
                raise RuntimeError("memory launch deadline exceeded; inspect native work before retry")
            result = subprocess.CompletedProcess(process.args, process.returncode, stdout)
    if result.returncode:
        raise RuntimeError("Compound refused or failed: " + " ".join(args[:2]))
    return json.loads(result.stdout) if capture else None


def work_id(home):
    return "work_memory_refresh_" + hashlib.sha256(str(home).encode()).hexdigest()[:16]


def validate_maintenance_work(record, home):
    if record.get("id") != work_id(home) or record.get("owner") != ACTOR:
        raise RuntimeError("maintenance work identity or owner mismatch")


def maintenance_work(binary, home):
    record = run(binary, ["work", "show", work_id(home), "--json"], capture=True)
    validate_maintenance_work(record, home)
    return record


def claim_ready(binary, home, record):
    validate_maintenance_work(record, home)
    if record.get("state") != "ready":
        raise RuntimeError("only ready maintenance work may be claimed")
    # The service claims as itself through native policy; no takeover or new work.
    run(binary, ["work", "claim", work_id(home), "--by", ACTOR])
    claimed = maintenance_work(binary, home)
    if claimed.get("state") != "claimed":
        raise RuntimeError("maintenance claim reservation changed")
    return claimed


def _tick_locked(binary, home, workspace, start=False, recovery_only=False, lock_fd=None):
    wid = work_id(home)
    record = maintenance_work(binary, home)
    if record.get("state") == "ready" and start:
        record = claim_ready(binary, home, record)
    state = record["state"]
    if state not in ("claimed", "verifying"):
        raise RuntimeError("maintenance work is " + state + "; no automatic takeover")
    args = ["work", "advance", wid, "--to", "running", "--by", ACTOR,
            "--execute", "--harness", "os-harness-entrypoints",
            "--workspace", str(workspace), "--timeout", "3m" if recovery_only else "8m"]
    if state == "verifying":
        args += ["--resume-verifying"]
    operation = "recover" if recovery_only else "refresh"
    run(binary, args + ["--", sys.executable, str(Path(__file__).resolve()), operation,
                         "--binary", str(binary), "--memory-home", str(home), "--workspace", str(workspace)],
        timeout=240 if recovery_only else 540, lock_fd=lock_fd)


def tick(binary, home, workspace, start=False, recovery_only=False):
    # Cover waiting/admission as well as running refresh. The kernel releases
    # this coordination lock after failures or process exit.
    lock_dir = home / "lock"
    lock_dir.mkdir(parents=True, exist_ok=True)
    with (lock_dir / "memory-refresh-launch.lock").open("a") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            print(json.dumps({"status": "already_running", "work_id": work_id(home)}))
            return
        _tick_locked(binary, home, workspace, start=start, recovery_only=recovery_only,
                     lock_fd=lock.fileno())


def setup(binary, home, workspace, config):
    # This operation is called inside the setup work's admitted subprocess.
    wid = work_id(home)
    probe = subprocess.run([str(binary), "work", "show", wid, "--json"],
                           capture_output=True, text=True)
    record = None
    if not probe.returncode:
        record = json.loads(probe.stdout)
        validate_maintenance_work(record, home)
        if record.get("state") not in ("ready", "claimed", "verifying"):
            raise RuntimeError("existing maintenance work is not available")
    # Refuse held or foreign work before changing configuration.
    run(binary, ["corpus", "configure", "--file", str(config)])
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
    elif record["state"] == "ready":
        claim_ready(binary, home, record)
    # Live hooks perform the first capture through the same native resume path.
    print(json.dumps({"status": "configured", "work_id": wid,
                      "trigger": "live PostToolUse and SessionEnd hooks",
                      "interval_seconds": 300}))


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("operation", choices=("setup", "tick", "refresh", "recover"))
    p.add_argument("--binary", type=Path, required=True)
    p.add_argument("--home", "--memory-home", dest="home", type=Path, required=True)
    p.add_argument("--workspace", type=Path, required=True)
    p.add_argument("--config", type=Path)
    p.add_argument("--start", action="store_true", help="explicitly claim this service's existing ready work")
    p.add_argument("--recovery-only", action="store_true", help="run admitted backend recovery without indexing")
    a = p.parse_args()
    if (a.start or a.recovery_only) and a.operation != "tick":
        p.error("--start and --recovery-only require tick")
    for value in (a.binary, a.home, a.workspace):
        if not value.is_absolute() or not value.exists():
            p.error("binary, home and workspace must be existing absolute paths")
    os.environ["COMPOUND_HOME"] = str(a.home)
    try:
        if a.operation == "refresh":
            refresh(a.binary, a.home)
        elif a.operation == "recover":
            recover(a.binary, a.home)
        elif a.operation == "tick":
            tick(a.binary, a.home, a.workspace, start=a.start, recovery_only=a.recovery_only)
        else:
            if a.config is None:
                p.error("setup requires --config with approved sources")
            setup(a.binary, a.home, a.workspace, a.config)
    except (RuntimeError, OSError, ValueError, subprocess.CalledProcessError, subprocess.TimeoutExpired) as e:
        print("memory service: " + str(e), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
