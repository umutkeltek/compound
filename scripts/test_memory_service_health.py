"""Bounded recovery fixtures: never contact or restart a real service."""
import datetime
import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch
from urllib.parse import urlunparse

spec = importlib.util.spec_from_file_location("memory_service", Path(__file__).with_name("memory-service.py"))
service = importlib.util.module_from_spec(spec)
spec.loader.exec_module(service)

LOCAL_ENDPOINT = urlunparse(("http", "127.0.0.1:11434", "/v1", "", "", ""))
READY = {"status": "ready", "backends": [{"status": "ready", "base_url": LOCAL_ENDPOINT, "identity": "ollama:fixture:2:f32"}]}
DOWN = {"status": "backend_unavailable", "backends": [{"status": "process_unavailable", "recovery_eligible": True, "base_url": LOCAL_ENDPOINT, "identity": "ollama:fixture:2:f32"}]}


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
        for status, base in [("model_missing", LOCAL_ENDPOINT), ("incompatible", LOCAL_ENDPOINT), ("model_changed", LOCAL_ENDPOINT), ("process_unavailable", "http://backend.invalid/v1")]:
            report = {"status": "backend_unavailable", "backends": [{"status": status, "base_url": base}]}
            with patch.object(service, "backend_health", return_value=report), patch.object(service.subprocess, "run") as run:
                with self.assertRaises(RuntimeError):
                    service.recover_backend(Path("/fixture/bin/compound"), Path("/fixture/home"))
                run.assert_not_called()

    def test_sanitized_or_generic_endpoint_without_ownership_never_restarts(self):
        for eligibility in (False, None):
            report = {"status": "backend_unavailable", "backends": [{"status": "process_unhealthy", "base_url": LOCAL_ENDPOINT}]}
            if eligibility is not None:
                report["backends"][0]["recovery_eligible"] = eligibility
            with patch.object(service, "backend_health", return_value=report), patch.object(service.subprocess, "run") as run:
                with self.assertRaisesRegex(RuntimeError, "approved service owner"):
                    service.recover_backend(Path("/fixture/bin/compound"), Path("/fixture/home"))
                run.assert_not_called()

    def test_persistent_budget_survives_new_service_process(self):
        now = service.time.time()
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



class HelperAdmissionTests(unittest.TestCase):
    binary = Path("/fixture/bin/compound")
    home = Path("/fixture/home")
    workspace = Path("/fixture/workspace")

    def record(self, state="ready"):
        return {"id": service.work_id(self.home), "owner": service.ACTOR, "state": state}

    def test_explicit_start_claims_only_existing_ready_work_then_executes(self):
        record = self.record()
        commands = []
        def command(binary, args, capture=False):
            commands.append(args)
            if args[:2] == ["work", "show"]:
                return dict(record)
            if args[:2] == ["work", "claim"]:
                self.assertEqual(args, ["work", "claim", record["id"], "--by", service.ACTOR])
                record["state"] = "claimed"
        with patch.object(service, "run", side_effect=command):
            service.tick(self.binary, self.home, self.workspace, start=True)
        writes = [args for args in commands if args[:2] != ["work", "show"]]
        self.assertEqual([args[1] for args in writes], ["claim", "advance"])
        self.assertNotIn("--resume-verifying", writes[-1])
        self.assertIn("refresh", writes[-1])
        self.assertNotIn("create", [args[1] for args in commands])

    def test_ready_without_explicit_start_and_held_with_start_refuse(self):
        for state, start in [("ready", False), ("held", True)]:
            with self.subTest(state=state), patch.object(service, "run", return_value=self.record(state)) as run:
                with self.assertRaises(RuntimeError):
                    service.tick(self.binary, self.home, self.workspace, start=start)
                self.assertEqual(run.call_count, 1)

    def test_tick_refuses_wrong_owner_or_work_id_before_mutation(self):
        for state in ("ready", "claimed", "verifying"):
            for field, value in [("owner", "unrelated-actor"), ("id", "work_unrelated")]:
                record = self.record(state)
                record[field] = value
                with self.subTest(state=state, field=field), patch.object(service, "run", return_value=record) as run:
                    with self.assertRaises(RuntimeError):
                        service.tick(self.binary, self.home, self.workspace, start=True)
                    self.assertEqual(run.call_count, 1)

    def test_setup_admits_existing_ready_owner_without_replacement_or_refresh(self):
        record = self.record()
        commands = []
        def command(binary, args, capture=False):
            commands.append(args)
            if args[:2] == ["work", "claim"]:
                record["state"] = "claimed"
            if args[:2] == ["work", "show"]:
                return dict(record)
        probe = subprocess.CompletedProcess([], 0, json.dumps(record))
        with patch.object(service.subprocess, "run", return_value=probe), patch.object(service, "run", side_effect=command):
            service.setup(self.binary, self.home, self.workspace, Path("/fixture/config.yaml"))
        self.assertEqual([args[1] for args in commands if args[0] == "work" and args[1] != "show"], ["claim"])
        self.assertNotIn("refresh", [item for args in commands for item in args])

    def test_setup_held_or_wrong_identity_does_not_reconfigure_or_claim(self):
        for patch_record in [{"state": "held"}, {"owner": "unrelated-actor"}, {"id": "work_unrelated"}]:
            record = self.record()
            record.update(patch_record)
            probe = subprocess.CompletedProcess([], 0, json.dumps(record))
            with self.subTest(record=patch_record), patch.object(service.subprocess, "run", return_value=probe), patch.object(service, "run") as run:
                with self.assertRaises(RuntimeError):
                    service.setup(self.binary, self.home, self.workspace, Path("/fixture/config.yaml"))
                run.assert_not_called()

    def test_explicit_start_cannot_bypass_native_claim_refusal(self):
        with patch.object(service, "run", side_effect=[self.record(), RuntimeError("native claim refused")]) as run:
            with self.assertRaisesRegex(RuntimeError, "native claim refused"):
                service.tick(self.binary, self.home, self.workspace, start=True)
        self.assertEqual(run.call_count, 2)
        self.assertEqual(run.call_args_list[-1].args[1][:2], ["work", "claim"])

    def test_recovery_refuses_attempt_for_a_different_home_work(self):
        with tempfile.TemporaryDirectory() as tmp:
            home = Path(tmp)
            (home / "run_attempts").mkdir()
            rid = "ratt_fixture_foreign"
            record = {"id": "work_unrelated", "owner": service.ACTOR, "state": "running",
                      "run_ids": ["act_fixture"], "policy_decision_id": "pol_fixture"}
            attempt = {"work_id": record["id"], "origin": "harness_execution",
                       "status": "running", "action_intent_id": "act_fixture"}
            (home / "run_attempts" / (rid + ".json")).write_text(json.dumps(attempt))
            with patch.dict(service.os.environ, {"COMPOUND_RUN_ATTEMPT_ID": rid}), patch.object(service, "run", return_value=record), patch.object(service, "recover_backend") as recover:
                with self.assertRaisesRegex(RuntimeError, "identity or owner"):
                    service.recover(self.binary, home)
                recover.assert_not_called()

    def test_recovery_tick_uses_current_native_attempt_and_never_full_refresh(self):
        with patch.object(service, "run", side_effect=[self.record("verifying"), None]) as run:
            service.tick(self.binary, self.home, self.workspace, recovery_only=True)
        args = run.call_args_list[-1].args[1]
        self.assertIn("--execute", args)
        self.assertIn("--resume-verifying", args)
        self.assertIn("recover", args)
        self.assertNotIn("refresh", args)
        with patch.object(service, "require_admitted_refresh") as guard, patch.object(service, "recover_backend") as recover, patch.object(service.subprocess, "run") as process:
            service.recover(self.binary, self.home)
            guard.assert_called_once_with(self.binary, self.home)
            recover.assert_called_once_with(self.binary, self.home)
            process.assert_not_called()
        with patch.object(service, "require_admitted_refresh", side_effect=RuntimeError("reservation changed")), patch.object(service, "recover_backend") as recover:
            with self.assertRaisesRegex(RuntimeError, "reservation changed"):
                service.recover(self.binary, self.home)
            recover.assert_not_called()



class ProcessObservationTests(unittest.TestCase):
    def observed(self, line, binary=Path("/fixture/bin/compound")):
        completed = subprocess.CompletedProcess(["/bin/ps"], 0, line + "\n", "")
        with patch.object(service.subprocess, "run", return_value=completed):
            return service.refresh_active(binary)

    def test_manual_index_and_global_home_refresh_are_active(self):
        for line, binary in [
            ("/fixture/bin/compound corpus index --lane fixture", Path("/fixture/bin/compound")),
            ("/fixture/bin/compound --home /fixture/home corpus refresh", Path("/fixture/bin/compound")),
            ("/fixture tools/memory-runtime --home=/fixture home corpus index --lane fixture", Path("/fixture tools/memory-runtime")),
            ("compound corpus refresh --home /fixture/home", Path("/fixture/bin/compound")),
        ]:
            with self.subTest(line=line):
                self.assertTrue(self.observed(line, binary))

    def test_health_helpers_and_embedded_command_text_are_not_ingestion(self):
        for line in [
            "/fixture/bin/compound corpus backend-health",
            "python3 /fixture/compound-memory-service refresh --binary /fixture/bin/compound",
            "/fixture/bin/compound work advance fixture --prompt corpus index",
            "/fixture/bin/compound --home /fixture/home corpus doctor --lane fixture",
        ]:
            with self.subTest(line=line):
                self.assertFalse(self.observed(line))

    def test_actual_manual_index_observation_prevents_kickstart(self):
        def command(args, **kwargs):
            if args[0] == "/bin/ps":
                return subprocess.CompletedProcess(args, 0, "/fixture/bin/compound corpus index --lane fixture\n", "")
            if args[1] == "print":
                return subprocess.CompletedProcess(args, 0, "program = /fixture/bin/ollama\nstate = running\n", "")
            self.fail("active manual indexing must exclude restart: " + str(args))
        with patch.object(service, "backend_health", return_value=DOWN), patch.object(service.subprocess, "run", side_effect=command), patch.object(service, "recovery_budget") as budget, patch.object(service, "require_admitted_refresh") as admitted:
            with self.assertRaisesRegex(RuntimeError, "active memory refresh"):
                service.recover_backend(Path("/fixture/bin/compound"), Path("/fixture/home"))
            budget.assert_not_called()
            admitted.assert_not_called()

if __name__ == "__main__":
    unittest.main()
