#!/usr/bin/env python3
"""Thin, read-only admission check and Switchback transport for a pinned Claude launch."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import selectors
import signal
import stat
import subprocess
import time
import sys
from datetime import datetime, timezone
from uuid import UUID

SCHEMA = "compound.native_account_pool.v1"
MAX_CONFIG = 32 * 1024
MAX_SETTINGS = 1024 * 1024
MAX_PRESENCE = 16 * 1024
DENIED = 75
SAFE_NAME = re.compile(r"[A-Za-z0-9][A-Za-z0-9_-]{0,63}\Z")
SHA = re.compile(r"[0-9a-f]{64}\Z")
KEYS = {"schema_version", "provider", "accounts", "settings_path", "settings_sha256",
        "switchback_cli", "compound_cli", "timeout_seconds"}
V2 = "compound.native_account_pool.v2"
V2_KEYS = {"schema_version", "provider", "profile_id", "pool_name", "profile_revision",
           "authority_revision", "accounts", "account_settings",
           "native_executable", "native_sha256", "switchback_cli",
           "compound_cli", "compound_home", "checker_path", "checker_sha256", "approval_policy",
           "sandbox", "timeout_seconds", "require_capture"}


class Refusal(Exception):
    def __init__(self, code):
        self.code = code


def refuse(code):
    raise Refusal(code)


def owned_bytes(path, limit):
    if not isinstance(path, str) or not os.path.isabs(path):
        refuse("path_invalid")
    try:
        fd = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_NONBLOCK", 0))
    except OSError:
        refuse("file_unavailable")
    try:
        info = os.fstat(fd)
        if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid()
                or info.st_mode & 0o022 or info.st_size > limit):
            refuse("file_unsafe")
        data = os.read(fd, limit + 1)
        if len(data) > limit:
            refuse("file_oversized")
        return data
    except OSError:
        refuse("file_unavailable")
    finally:
        os.close(fd)


def digest(data):
    return hashlib.sha256(data).hexdigest()


def load_config(path, expected):
    raw = owned_bytes(path, MAX_CONFIG)
    revision = digest(raw)
    if expected is not None and revision != expected:
        refuse("config_drift")
    try:
        config = json.loads(raw)
    except (ValueError, UnicodeDecodeError):
        refuse("config_invalid")
    if not isinstance(config, dict):
        refuse("config_invalid")
    v2 = config.get("schema_version") == V2
    if set(config) != (V2_KEYS if v2 else KEYS):
        refuse("config_invalid")
    if v2:
        if config["provider"] != "codex" or config["require_capture"] is not True:
            refuse("config_invalid")
        for key in ("profile_id", "pool_name"):
            if not isinstance(config[key], str) or not SAFE_NAME.fullmatch(config[key]):
                refuse("config_invalid")
        for key in ("profile_revision", "authority_revision"):
            if not isinstance(config[key], str) or not config[key].startswith("sha256:") or not SHA.fullmatch(config[key][7:]):
                refuse("config_invalid")
        for key in ("native_sha256", "checker_sha256"):
            if not isinstance(config[key], str) or not SHA.fullmatch(config[key]):
                refuse("config_invalid")
        if config["approval_policy"] not in ("never", "on-request") or config["sandbox"] not in ("read-only", "workspace-write"):
            refuse("posture_unsupported")
    elif config["schema_version"] != SCHEMA or config["provider"] != "claude":
        refuse("config_invalid")
    accounts = config["accounts"]
    if (not isinstance(accounts, list) or not 1 <= len(accounts) <= 16
            or any(not isinstance(a, str) or not SAFE_NAME.fullmatch(a) for a in accounts)
            or len(set(accounts)) != len(accounts)):
        refuse("accounts_invalid")
    for name in (("native_executable", "checker_path", "switchback_cli", "compound_cli")
                 if v2 else ("settings_path", "switchback_cli", "compound_cli")):
        if not isinstance(config[name], str) or not os.path.isabs(config[name]):
            refuse("path_invalid")
    if v2:
        home = config["compound_home"]
        if not isinstance(home, str) or not os.path.isabs(home):
            refuse("path_invalid")
        try:
            home_info = os.stat(home)
        except OSError:
            refuse("home_unavailable")
        if (not stat.S_ISDIR(home_info.st_mode) or home_info.st_uid not in (os.getuid(), 0)
                or home_info.st_mode & 0o022):
            refuse("home_unsafe")
        expected = config["account_settings"]
        if (not isinstance(expected, dict) or set(expected) != set(accounts)
                or any(not isinstance(value, str) or
                       (value != "missing" and
                        (not value.startswith("sha256:") or not SHA.fullmatch(value[7:])))
                       for value in expected.values())):
            refuse("settings_attestation_invalid")
        if os.path.realpath(config["checker_path"]) != os.path.realpath(__file__):
            refuse("checker_invalid")
        if digest(owned_bytes(config["checker_path"], MAX_SETTINGS)) != config["checker_sha256"]:
            refuse("checker_drift")
    elif not isinstance(config["settings_sha256"], str) or not SHA.fullmatch(config["settings_sha256"]):
        refuse("settings_digest_invalid")
    if type(config["timeout_seconds"]) is not int or not 1 <= config["timeout_seconds"] <= 2700:
        refuse("timeout_invalid")
    for name in ("switchback_cli", "compound_cli"):
        try:
            link = os.lstat(config[name])
            info = os.stat(config[name])
        except OSError:
            refuse("executable_unavailable")
        if (link.st_uid not in (os.getuid(), 0)
                or info.st_uid not in (os.getuid(), 0)
                or not stat.S_ISREG(info.st_mode)
                or info.st_mode & 0o022
                or not info.st_mode & 0o111):
            refuse("executable_invalid")

    return config, revision


def parse_time(value):
    if not isinstance(value, str):
        refuse("presence_invalid")
    try:
        result = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        refuse("presence_invalid")
    if result.tzinfo is None:
        refuse("presence_invalid")
    return result.astimezone(timezone.utc)


ANCHOR = "import os, signal, subprocess, sys\ntry:\n    child = subprocess.Popen([sys.argv[1], \"policy\", \"presence\", \"show\", \"--json\"])\n    status = child.wait()\nexcept OSError:\n    status = 127\nos.write(int(sys.argv[2]), str(status).encode(\"ascii\"))\nos.close(int(sys.argv[2]))\nos.close(1)\nos.close(2)\nsignal.pause()\n"


def presence_output(cli, home=None):
    cancellation = {"armed": False, "pending": False}

    def on_cancel(signum, frame):
        if cancellation["armed"]:
            refuse("presence_cancelled")
        cancellation["pending"] = True

    saved = []
    try:
        for signum in (signal.SIGTERM, signal.SIGINT):
            saved.append((signum, signal.signal(signum, on_cancel)))
        if cancellation["pending"]:
            refuse("presence_cancelled")
        result = _presence_output(cli, cancellation, home)
        if cancellation["pending"]:
            refuse("presence_cancelled")
        return result
    except ValueError:
        refuse("presence_unavailable")
    finally:
        for signum, old in reversed(saved):
            signal.signal(signum, old)


def _presence_output(cli, cancellation, home):
    """Read bounded presence output with a live process-group anchor."""
    if not hasattr(os, "killpg") or not hasattr(signal, "pause"):
        refuse("presence_unavailable")
    try:
        status_read, status_write = os.pipe()
    except OSError:
        refuse("presence_unavailable")
    process = None
    status_stream = None
    reader = None
    reaped = False
    try:
        process = subprocess.Popen(
            [sys.executable, "-c", ANCHOR, cli, str(status_write)],
            stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            pass_fds=(status_write,), start_new_session=True,
            env=dict(os.environ, COMPOUND_HOME=home) if home else None)
        cancellation["armed"] = True
        if cancellation["pending"]:
            refuse("presence_cancelled")
        os.close(status_write)
        status_write = None
        status_stream = os.fdopen(status_read, "rb", buffering=0)
        status_read = None
        deadline = time.monotonic() + 3
        reader = selectors.DefaultSelector()
        output = {process.stdout: bytearray(), process.stderr: bytearray(),
                  status_stream: bytearray()}
        for stream in output:
            os.set_blocking(stream.fileno(), False)
            reader.register(stream, selectors.EVENT_READ)
        while reader.get_map():
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                refuse("presence_unavailable")
            events = reader.select(remaining)
            if not events:
                refuse("presence_unavailable")
            for key, _ in events:
                chunk = os.read(key.fileobj.fileno(), 4096)
                if not chunk:
                    reader.unregister(key.fileobj)
                    continue
                output[key.fileobj].extend(chunk)
                limit = 32 if key.fileobj is status_stream else MAX_PRESENCE
                if len(output[key.fileobj]) > limit:
                    refuse("presence_unavailable")
        status_bytes = bytes(output[status_stream])
        if not re.fullmatch(rb"-?[0-9]{1,3}", status_bytes):
            refuse("presence_unavailable")
        # The anchor stays alive after reporting status. Kill its group before
        # reaping the leader, including quiet descendants with closed pipes.
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        cancellation["armed"] = False
        try:
            process.wait(timeout=1)
            reaped = True
        except subprocess.TimeoutExpired:
            refuse("presence_unavailable")
        if int(status_bytes):
            refuse("presence_unavailable")
        return bytes(output[process.stdout])
    except OSError:
        refuse("presence_unavailable")
    finally:
        cancellation["armed"] = False
        if reader is not None:
            reader.close()
        if process is not None and not reaped:
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            try:
                process.wait(timeout=1)
            except subprocess.TimeoutExpired:
                pass
        if process is not None:
            process.stdout.close()
            process.stderr.close()
        if status_stream is not None:
            status_stream.close()
        if status_read is not None:
            os.close(status_read)
        if status_write is not None:
            os.close(status_write)


def check_presence(cli, home=None):
    raw = presence_output(cli, home)
    try:
        data = json.loads(raw)
    except (ValueError, UnicodeDecodeError):
        refuse("presence_invalid")
    if not isinstance(data, dict) or data.get("effective") != "away":
        refuse("presence_not_away")
    window = data.get("presence")
    if not isinstance(window, dict) or window.get("state") != "away":
        refuse("presence_invalid")
    start, end = parse_time(window.get("from")), parse_time(window.get("through"))
    now = datetime.now(timezone.utc)
    if not start <= now < end:
        refuse("presence_window_closed")


def check(config):
    if config["schema_version"] == SCHEMA:
        raw = owned_bytes(config["settings_path"], MAX_SETTINGS)
        if digest(raw) != config["settings_sha256"]:
            refuse("settings_drift")
    check_presence(config["compound_cli"], config.get("compound_home"))


def native_args(argv):
    # ClaudeAdapter builds exactly this shape. The final argument is opaque prompt
    # text: even if it resembles a flag, it is not interpreted or logged here.
    if not argv or argv[0] != "--print":
        refuse("native_argv_invalid")
    if len(argv) == 6 and argv[1] == "--permission-mode":
        if argv[2] not in ("plan", "default", "acceptEdits"):
            refuse("native_argv_invalid")
        session = argv[4] if argv[3] == "--session-id" else None
    elif len(argv) == 5 and argv[1] == "--dangerously-skip-permissions":
        session = argv[3] if argv[2] == "--session-id" else None
    else:
        refuse("native_argv_invalid")
    if session is None or not argv[-1]:
        refuse("native_argv_invalid")
    try:
        parsed = UUID(session)
    except (ValueError, AttributeError):
        refuse("native_argv_invalid")
    if str(parsed) != session or parsed.version != 4:
        refuse("native_argv_invalid")
    return argv


class QuietParser(argparse.ArgumentParser):
    def error(self, message):
        refuse("cli_invalid")


def main():
    parser = QuietParser(description=__doc__)
    parser.add_argument("--config", required=True)
    parser.add_argument("--expected-config-sha256")
    group = parser.add_mutually_exclusive_group()
    group.add_argument("--check", action="store_true")
    group.add_argument("--plan", action="store_true")
    parser.add_argument("native_argv", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    if args.expected_config_sha256 is not None and not SHA.fullmatch(args.expected_config_sha256):
        refuse("config_digest_invalid")
    config, revision = load_config(args.config, args.expected_config_sha256)
    if args.check:
        if args.native_argv:
            refuse("native_argv_invalid")
        check(config)
        print(json.dumps({"allow": True, "schema_version": SCHEMA, "config_sha256": revision}))
        return
    if args.plan:
        if args.native_argv:
            refuse("native_argv_invalid")
        check(config)
        result = {"ready": True, "admission": "away_window_and_pinned_config",
                  "schema_version": config["schema_version"], "config_sha256": revision,
                  "pool_count": len(config["accounts"])}
        if config["schema_version"] == SCHEMA:
            result["settings_sha256"] = config["settings_sha256"]
        print(json.dumps(result))
        return
    if not args.native_argv or args.native_argv[0] != "--":
        refuse("native_argv_invalid")
    if config["schema_version"] == V2:
        refuse("codex_direct_launch_unsupported")
    native = native_args(args.native_argv[1:])
    check(config)
    callback = [sys.executable, str(Path(__file__).resolve()), "--config",
                args.config, "--expected-config-sha256", revision, "--check"]
    launch = [config["switchback_cli"], "account", "run", "--provider", "claude",
              "--pool", ",".join(config["accounts"]), "--expected-settings-sha256",
              config["settings_sha256"], "--timeout", str(config["timeout_seconds"]),
              "--admission-check-command", json.dumps(callback, separators=(",", ":")),
              "--", *native]
    os.execvpe(config["switchback_cli"], launch, os.environ)


if __name__ == "__main__":
    try:
        main()
    except Refusal as error:
        print(json.dumps({"allow": False, "error": error.code}), file=sys.stderr)
        sys.exit(DENIED)
