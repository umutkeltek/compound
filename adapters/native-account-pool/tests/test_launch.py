import hashlib
import json
import os
import signal
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest
from datetime import datetime, timedelta, timezone
from uuid import uuid4

LAUNCH = Path(__file__).resolve().parents[1] / "launch.py"


class LaunchTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.settings = self.root / "settings.json"
        self.settings.write_text("{}")
        self.settings.chmod(0o600)
        self.presence = self.root / "presence.json"
        self.marker = self.root / "switchback.json"
        self.survivor = self.root / "survived.txt"
        self.started = self.root / "started.txt"
        self.compound = self.root / "compound"
        self.compound.write_text("""#!/usr/bin/env python3
import os, subprocess, sys, time
from pathlib import Path
assert sys.argv[1:] == ['policy','presence','show','--json']
kind = os.environ.get('FAKE_PRESENCE')
if kind == 'nonzero': sys.exit(9)
if kind == 'timeout': time.sleep(5)
if kind == 'closed_pipes':
    os.close(1); os.close(2); time.sleep(10)
if kind == 'cancel_child':
    subprocess.Popen([sys.executable, '-c',
        "import os,time;from pathlib import Path;time.sleep(1.5);Path(os.environ['SURVIVOR_MARKER']).write_text('survived')"],
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    Path(os.environ['STARTED_MARKER']).write_text('started')
    time.sleep(30)
if kind == 'quiet_child':
    subprocess.Popen([sys.executable, '-c',
        "import os,time;from pathlib import Path;time.sleep(1.5);Path(os.environ['SURVIVOR_MARKER']).write_text('survived')"],
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
if kind == 'held_pipe':
    subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(30)'])
if kind == 'flood_stdout':
    os.write(1, b'x' * 20000); time.sleep(10)
if kind == 'flood_stderr':
    os.write(2, b'x' * 20000); time.sleep(10)
if kind == 'malformed': print('{'); sys.exit(0)
print(Path(os.environ['PRESENCE_FILE']).read_text())
""")
        self.compound.chmod(0o700)
        self.switchback = self.root / "switchback"
        self.switchback.write_text("""#!/usr/bin/env python3
import json, os, subprocess, sys
from pathlib import Path
argv = sys.argv[1:]
check = json.loads(argv[argv.index('--admission-check-command') + 1])
result = subprocess.run(check, capture_output=True, text=True, timeout=5, env=os.environ)
if result.returncode or json.loads(result.stdout).get('allow') is not True:
    sys.exit(75)
Path(os.environ['MARKER_FILE']).write_text(json.dumps(argv))
print('native-output')
""")
        self.switchback.chmod(0o700)
        self.config_path = self.root / "config.json"
        self.config = {
            "schema_version": "compound.native_account_pool.v1",
            "provider": "claude", "accounts": ["one", "two"],
            "settings_path": str(self.settings),
            "settings_sha256": hashlib.sha256(self.settings.read_bytes()).hexdigest(),
            "switchback_cli": str(self.switchback),
            "compound_cli": str(self.compound),
            "timeout_seconds": 90,
        }
        self.write_config()
        self.set_window()
        self.native = ["--print", "--permission-mode", "plan", "--session-id", str(uuid4()), "private prompt"]
        self.env = dict(os.environ, PRESENCE_FILE=str(self.presence), MARKER_FILE=str(self.marker), SURVIVOR_MARKER=str(self.survivor), STARTED_MARKER=str(self.started))

    def write_config(self):
        self.config_path.write_text(json.dumps(self.config))
        self.config_path.chmod(0o600)

    def set_window(self, *, start=-60, end=60, effective="away"):
        now = datetime.now(timezone.utc)
        self.presence.write_text(json.dumps({"effective": effective, "presence": {
            "state": "away", "from": (now + timedelta(seconds=start)).isoformat(),
            "through": (now + timedelta(seconds=end)).isoformat()}}))

    def run_launch(self, *args, env=None):
        return subprocess.run([sys.executable, str(LAUNCH), "--config", str(self.config_path), *args],
                              text=True, capture_output=True, env=env or self.env, timeout=12)

    def assert_denied(self, result, code=None):
        self.assertEqual(result.returncode, 75, result.stdout + result.stderr)
        self.assertFalse(self.marker.exists())
        self.assertNotIn("private prompt", result.stdout + result.stderr)
        if code:
            self.assertEqual(json.loads(result.stderr)["error"], code)

    def test_launch_preserves_native_argv_and_callback_rechecks(self):
        result = self.run_launch("--", *self.native)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "native-output\n")
        args = json.loads(self.marker.read_text())
        self.assertEqual(args[-len(self.native):], self.native)
        self.assertEqual(args[args.index("--provider") + 1], "claude")
        self.assertEqual(args[args.index("--pool") + 1], "one,two")
        self.assertNotIn("--dangerously-skip-permissions", args)
        check = json.loads(args[args.index("--admission-check-command") + 1])
        self.assertEqual(json.loads(subprocess.check_output(check, env=self.env))["allow"], True)
        self.set_window(effective="present")
        stale = subprocess.run(check, env=self.env, capture_output=True, text=True)
        self.assertEqual(stale.returncode, 75)
        self.assertEqual(json.loads(stale.stderr)["error"], "presence_not_away")
        self.set_window()
        self.config["accounts"] = ["other"]
        self.write_config()
        drift = subprocess.run(check, env=self.env, capture_output=True, text=True)
        self.assertEqual(drift.returncode, 75)
        self.assertEqual(json.loads(drift.stderr)["error"], "config_drift")

    def test_plan_and_check_are_read_only(self):
        plan = self.run_launch("--plan")
        self.assertEqual(plan.returncode, 0, plan.stderr)
        data = json.loads(plan.stdout)
        self.assertEqual(data["pool_count"], 2)
        self.assertTrue(data["ready"])
        self.assertNotIn("private prompt", plan.stdout)
        check = self.run_launch("--check")
        self.assertEqual(json.loads(check.stdout)["allow"], True)
        self.assertFalse(self.marker.exists())

    def test_presence_failures_stop_before_switchback(self):
        for start, end, effective, code in [
            (60, 120, "away", "presence_window_closed"),
            (-120, -60, "away", "presence_window_closed"),
            (-60, 60, "present", "presence_not_away"),
        ]:
            with self.subTest(code=code, start=start):
                self.set_window(start=start, end=end, effective=effective)
                self.assert_denied(self.run_launch("--", *self.native), code)
        self.set_window()
        for kind, code in [("malformed", "presence_invalid"), ("nonzero", "presence_unavailable"),
                           ("timeout", "presence_unavailable")]:
            with self.subTest(kind=kind):
                self.assert_denied(self.run_launch("--", *self.native,
                                  env=dict(self.env, FAKE_PRESENCE=kind)), code)

    def test_presence_probe_bounds_both_pipes_and_descendants(self):
        for kind in ("flood_stdout", "flood_stderr", "closed_pipes", "held_pipe"):
            with self.subTest(kind=kind):
                started = time.monotonic()
                self.assert_denied(self.run_launch("--", *self.native,
                                  env=dict(self.env, FAKE_PRESENCE=kind)),
                                  "presence_unavailable")
                self.assertLess(time.monotonic() - started, 5)

    def test_successful_leader_cannot_leave_quiet_descendant(self):
        result = self.run_launch("--plan", env=dict(self.env, FAKE_PRESENCE="quiet_child"))
        self.assertEqual(result.returncode, 0, result.stderr)
        time.sleep(1.8)
        self.assertFalse(self.survivor.exists(), "presence descendant survived leader")

    def test_sigterm_cancels_presence_group(self):
        env = dict(self.env, FAKE_PRESENCE="cancel_child")
        process = subprocess.Popen([sys.executable, str(LAUNCH), "--config",
                                    str(self.config_path), "--plan"],
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                   text=True, env=env)
        try:
            deadline = time.monotonic() + 3
            while not self.started.exists() and time.monotonic() < deadline:
                time.sleep(0.02)
            self.assertTrue(self.started.exists(), "presence child never started")
            os.kill(process.pid, signal.SIGTERM)
            out, err = process.communicate(timeout=5)
            self.assertEqual(process.returncode, 75, out + err)
            self.assertEqual(json.loads(err)["error"], "presence_cancelled")
            time.sleep(1.8)
            self.assertFalse(self.survivor.exists(), "presence descendant survived cancellation")
        finally:
            if process.poll() is None:
                process.kill()
                process.wait(timeout=2)

    def test_executables_must_be_owned_safe_regular_targets(self):
        alias = self.root / "sb-alias"
        alias.symlink_to(self.switchback)
        self.config["switchback_cli"] = str(alias)
        self.write_config()
        self.assertEqual(self.run_launch("--plan").returncode, 0)
        self.switchback.chmod(0o722)
        self.assert_denied(self.run_launch("--plan"), "executable_invalid")
        self.switchback.chmod(0o700)
        self.compound.chmod(0o702)
        self.assert_denied(self.run_launch("--plan"), "executable_invalid")

    def test_settings_and_config_drift_and_unsafe_files(self):
        self.settings.write_text('{"changed":true}')
        self.assert_denied(self.run_launch("--", *self.native), "settings_drift")
        self.settings.write_text("{}")
        self.settings.chmod(0o666)
        self.assert_denied(self.run_launch("--plan"), "file_unsafe")
        self.settings.chmod(0o600)
        expected = hashlib.sha256(self.config_path.read_bytes()).hexdigest()
        self.config["accounts"] = ["three"]
        self.write_config()
        self.assert_denied(self.run_launch("--expected-config-sha256", expected, "--check"), "config_drift")
        self.config_path.chmod(0o666)
        self.assert_denied(self.run_launch("--plan"), "file_unsafe")

    def test_config_symlink_and_oversize_refused(self):
        link = self.root / "link.json"
        link.symlink_to(self.config_path)
        result = subprocess.run([sys.executable, str(LAUNCH), "--config", str(link), "--plan"],
                                capture_output=True, text=True, env=self.env)
        self.assert_denied(result)
        self.config_path.write_text("x" * 40000)
        self.config_path.chmod(0o600)
        self.assert_denied(self.run_launch("--plan"), "file_unsafe")

    def test_prompt_is_opaque_and_parser_errors_do_not_echo_it(self):
        native = ["--print", "--permission-mode", "plan", "--session-id",
                  str(uuid4()), "--dangerously-skip-permissions"]
        result = self.run_launch("--", *native)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(self.marker.read_text())[-len(native):], native)
        self.marker.unlink()
        malformed = self.run_launch("--invalid-flag", "private prompt")
        self.assert_denied(malformed, "cli_invalid")

    def test_codex_v2_is_check_only_and_pins_account_map(self):
        self.config = {
            "schema_version": "compound.native_account_pool.v2",
            "provider": "codex", "profile_id": "work_profile",
            "pool_name": "work_pool",
            "profile_revision": "sha256:" + "a" * 64,
            "authority_revision": "sha256:" + "b" * 64,
            "accounts": ["one", "two"],
            "account_settings": {"one": "sha256:" + "c" * 64, "two": "missing"},
            "native_executable": str(self.switchback),
            "native_sha256": "d" * 64,
            "switchback_cli": str(self.switchback),
            "compound_cli": str(self.compound),
            "compound_home": str(self.root),
            "checker_path": str(LAUNCH),
            "checker_sha256": hashlib.sha256(LAUNCH.read_bytes()).hexdigest(),
            "approval_policy": "never", "sandbox": "read-only",
            "timeout_seconds": 90, "require_capture": True,
        }
        self.write_config()
        self.assertEqual(self.run_launch("--plan").returncode, 0)
        self.assertEqual(json.loads(self.run_launch("--check").stdout)["allow"], True)
        self.assert_denied(self.run_launch("--", *self.native), "codex_direct_launch_unsupported")
        self.config["account_settings"].pop("two")
        self.write_config()
        self.assert_denied(self.run_launch("--check"), "settings_attestation_invalid")

    def test_native_session_and_permission_shape(self):
        for native in [
            ["--print", "--permission-mode", "plan", "private prompt"],
            ["--print", "--session-id", "bad", "--permission-mode", "plan", "private prompt"],
            ["--print", "--session-id", str(uuid4()), "--session-id", str(uuid4()),
             "--permission-mode", "plan", "private prompt"],
            ["--print", "--session-id", str(uuid4()), "--permission-mode", "plan",
             "--dangerously-skip-permissions", "private prompt"],
            ["--print", "--session-id", str(uuid4()), "private prompt"],
        ]:
            with self.subTest(native=native[:-1]):
                self.assert_denied(self.run_launch("--", *native), "native_argv_invalid")


if __name__ == "__main__":
    unittest.main()
