"""Bounded recovery fixtures: never contact or restart a real service."""
import datetime
import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("memory_service", Path(__file__).with_name("memory-service.py"))
service = importlib.util.module_from_spec(spec)
spec.loader.exec_module(service)

READY = {"status": "ready", "backends": [{"status": "ready", "base_url": "http://127.0.0.1:11434/v1", "identity": "ollama:fixture:2:f32"}]}
DOWN = {"status": "backend_unavailable", "backends": [{"status": "process_unavailable", "base_url": "http://127.0.0.1:11434/v1", "identity": "ollama:fixture:2:f32"}]}


class RecoveryTests(unittest.TestCase):
    def test_ready_never_restarts_or_reads_history(self):
        with patch.object(service, "backend_health", return_value=READY), patch.object(service.subprocess, "run") as run:
            service.recover_backend(Path("/fixture/bin/compound"), Path("/fixture/home"))
            run.assert_not_called()

    def invoke_down(self, after=None, active=False, budget=None, restart_code=0):
        def command(args, **kwargs):
            if args[1] == "print":
                return subprocess.CompletedProcess(args, 0, "program = /fixture/bin/ollama\nstate = running\n", "")
            if args[1] == "kickstart":
                self.kills += 1
                return subprocess.CompletedProcess(args, restart_code)
            self.fail("unexpected command: " + str(args))
        self.kills = 0
        return (
            patch.object(service, "backend_health", side_effect=[DOWN] + (after or [DOWN, DOWN, DOWN])),
            patch.object(service, "refresh_active", return_value=active),
            patch.object(service, "recovery_budget", side_effect=budget),
            patch.object(service.subprocess, "run", side_effect=command),
            patch.object(service.time, "sleep"),
            patch.object(service, "require_admitted_refresh"),
        )

    def test_one_restart_then_retrieval_can_continue(self):
        patches = self.invoke_down([READY])
        with patches[0] as probe, patches[1], patches[2], patches[3], patches[4], patches[5]:
            service.recover_backend(Path("/fixture/bin/compound"), Path("/fixture/home"))
            self.assertEqual(probe.call_count, 2)
            self.assertEqual(self.kills, 1)

    def test_exhaustion_stops_after_one_restart_three_probes(self):
        patches = self.invoke_down()
        with patches[0] as probe, patches[1], patches[2], patches[3], patches[4], patches[5]:
            with self.assertRaisesRegex(RuntimeError, "exhausted"):
                service.recover_backend(Path("/fixture/bin/compound"), Path("/fixture/home"))
            self.assertEqual(self.kills, 1)
            self.assertEqual(probe.call_count, 4)

    def test_active_refresh_and_cooldown_never_restart(self):
        for active, budget, expected in [(True, None, "active memory refresh"), (False, RuntimeError("cooldown"), "cooldown")]:
            patches = self.invoke_down(active=active, budget=budget)
            with patches[0], patches[1], patches[2], patches[3], patches[4], patches[5]:
                with self.assertRaisesRegex(RuntimeError, expected):
                    service.recover_backend(Path("/fixture/bin/compound"), Path("/fixture/home"))
                self.assertEqual(self.kills, 0)

    def test_missing_incompatible_or_custom_owner_never_restart(self):
        for status, base in [("model_missing", "http://127.0.0.1:11434/v1"), ("incompatible", "http://127.0.0.1:11434/v1"), ("process_unavailable", "http://backend.invalid/v1")]:
            report = {"status": "backend_unavailable", "backends": [{"status": status, "base_url": base}]}
            with patch.object(service, "backend_health", return_value=report), patch.object(service.subprocess, "run") as run:
                with self.assertRaises(RuntimeError):
                    service.recover_backend(Path("/fixture/bin/compound"), Path("/fixture/home"))
                run.assert_not_called()

    def test_persistent_budget_survives_new_service_process(self):
        now = 2000000000
        with tempfile.TemporaryDirectory() as tmp:
            home = Path(tmp)
            directory = home / "run_attempts"
            directory.mkdir()
            ids = []
            def add(seconds):
                rid = "ratt_fixture_" + str(len(ids))
                ids.append(rid)
                when = datetime.datetime.fromtimestamp(now - seconds, datetime.timezone.utc).isoformat()
                (directory / (rid + ".json")).write_text(json.dumps({"created_at": when, "origin": "harness_execution", "status": "failed"}))
                (directory / (rid + ".transcript")).write_text("stdout: " + service.RECOVERY_MARKER + str(now - seconds) + "\n")
            with patch.object(service, "run", side_effect=lambda *a, **k: {"run_attempt_ids": ids}):
                service.recovery_budget(Path("/fixture/bin/compound"), home, now)
                add(60)
                with self.assertRaisesRegex(RuntimeError, "cooldown"):
                    service.recovery_budget(Path("/fixture/bin/compound"), home, now)
                add(3600)
                add(7200)
                with self.assertRaisesRegex(RuntimeError, "daily"):
                    service.recovery_budget(Path("/fixture/bin/compound"), home, now)

    def test_refresh_outage_is_error_not_empty_success(self):
        with patch.object(service, "require_admitted_refresh"), patch.object(service, "recover_backend", side_effect=RuntimeError("backend_unavailable")), patch.object(service.subprocess, "run") as run:
            with self.assertRaisesRegex(RuntimeError, "backend_unavailable"):
                service.refresh(Path("/fixture/bin/compound"), Path("/fixture/home"))
            run.assert_not_called()

    def test_index_failure_is_reported_separately(self):
        with patch.object(service, "require_admitted_refresh"), patch.object(service, "recover_backend"), patch.object(service.subprocess, "run", return_value=subprocess.CompletedProcess([], 1)):
            with self.assertRaisesRegex(RuntimeError, "indexing_degraded"):
                service.refresh(Path("/fixture/bin/compound"), Path("/fixture/home"))

    def test_admission_requires_current_native_work_and_attempt(self):
        now = datetime.datetime.now(datetime.timezone.utc).isoformat()
        with tempfile.TemporaryDirectory() as tmp:
            home = Path(tmp)
            (home / "run_attempts").mkdir()
            rid = "ratt_fixture_current"
            path = home / "run_attempts" / (rid + ".json")
            record = {"id": service.work_id(home), "state": "running", "owner": service.ACTOR,
                      "run_ids": ["act_fixture"], "policy_decision_id": "pol_fixture"}
            attempt = {"work_id": record["id"], "origin": "harness_execution", "status": "running",
                       "action_intent_id": "act_fixture", "created_at": now}
            path.write_text(json.dumps(attempt))
            with patch.dict(service.os.environ, {"COMPOUND_RUN_ATTEMPT_ID": rid}), patch.object(service, "run", return_value=record):
                service.require_admitted_refresh(Path("/fixture/bin/compound"), home)
                record["run_ids"] = ["act_changed"]
                with self.assertRaisesRegex(RuntimeError, "reservation changed"):
                    service.require_admitted_refresh(Path("/fixture/bin/compound"), home)
            with patch.dict(service.os.environ, {}, clear=True):
                with self.assertRaisesRegex(RuntimeError, "governed"):
                    service.require_admitted_refresh(Path("/fixture/bin/compound"), home)


if __name__ == "__main__":
    unittest.main()
