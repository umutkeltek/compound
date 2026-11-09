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
    if not isinstance(config, dict) or set(config) != KEYS:
        refuse("config_invalid")
    if config["schema_version"] != SCHEMA or config["provider"] != "claude":
        refuse("config_invalid")
    accounts = config["accounts"]
    if (not isinstance(accounts, list) or not 1 <= len(accounts) <= 16
            or any(not isinstance(a, str) or not SAFE_NAME.fullmatch(a) for a in accounts)
            or len(set(accounts)) != len(accounts)):
        refuse("accounts_invalid")
    for name in ("settings_path", "switchback_cli", "compound_cli"):
        if not isinstance(config[name], str) or not os.path.isabs(config[name]):
            refuse("path_invalid")
    if not isinstance(config["settings_sha256"], str) or not SHA.fullmatch(config["settings_sha256"]):
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


def presence_output(cli):
    """Read both pipes to EOF under one deadline without reaping before group cleanup."""
    try:
        process = subprocess.Popen(
            [cli, "policy", "presence", "show", "--json"],
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
    except OSError:
        refuse("presence_unavailable")
    deadline = time.monotonic() + 3
    reaped = False
    reader = selectors.DefaultSelector()
    output = {process.stdout: bytearray(), process.stderr: bytearray()}
    try:
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
                if len(output[key.fileobj]) > MAX_PRESENCE:
                    refuse("presence_unavailable")
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            refuse("presence_unavailable")
        try:
            status = process.wait(timeout=remaining)
            reaped = True
        except subprocess.TimeoutExpired:
            refuse("presence_unavailable")
        if status:
            refuse("presence_unavailable")
        return bytes(output[process.stdout])
    except OSError:
        refuse("presence_unavailable")
    finally:
        reader.close()
        if not reaped:
            # The session leader has not been waited on, so its PGID cannot be
            # reused even if it exited while a grandchild held a pipe open.
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            try:
                process.wait(timeout=1)
            except subprocess.TimeoutExpired:
                pass
        process.stdout.close()
        process.stderr.close()


def check_presence(cli):
    raw = presence_output(cli)
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
    raw = owned_bytes(config["settings_path"], MAX_SETTINGS)
    if digest(raw) != config["settings_sha256"]:
        refuse("settings_drift")
    check_presence(config["compound_cli"])


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
        print(json.dumps({"ready": True, "admission": "away_window_and_settings_match",
                          "schema_version": SCHEMA, "config_sha256": revision,
                          "settings_sha256": config["settings_sha256"],
                          "pool_count": len(config["accounts"])}))
        return
    if not args.native_argv or args.native_argv[0] != "--":
        refuse("native_argv_invalid")
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
