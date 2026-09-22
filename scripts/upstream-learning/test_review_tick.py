import copy
import datetime as dt
import json
from pathlib import Path
import tempfile
import time
import unittest
from unittest.mock import patch

import review_tick as review
from test_upstream_scout import Fake, cfg


def lane(provider):
    return {"model_id": "fixture-" + provider, "provider_route": provider,
            "harness": "claude_" + provider, "gate_role": "eligible",
            "capacity": {"state": "alive", "source_installed": True}}


class GovernedFixture:
    """Stateful boundary double: a run creates the canonical attempt/response."""
    def __init__(self, root, observation):
        self.home, self.observation = root / "home", observation
        self.home.mkdir()
        self.works, self.calls = {}, []
        self.routes = [lane("minimax"), lane("qwen")]
        self.fail_record, self.allow, self.invalid_quote = False, True, False
        self.deadline = time.monotonic() + 450
        self.response = {"summary": "A testable candidate.", "claims": [{
            "hypothesis": "Preserve applicability when decoding a limit.",
            "citation": "find_abcdef", "source_quote": observation["bodies"]["find_abcdef"],
            "relation": "addresses", "consumer": "quota-owner",
            "falsifier": "A scoped fixture incorrectly admits an exhausted model.",
            "expected_benefit": "May reduce incorrect admission; unmeasured.",
            "novelty": "candidate", "verdict": "supported"}]}

    def put(self, path, value):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(value))

    def arg(self, args, key):
        return args[args.index(key) + 1]

    def call(self, *args):
        self.calls.append(args)
        if args == ("route", "--help"):
            return "plan --capability-class C --read-only"
        identity = args[2] if len(args) > 2 else ""
        if args[:2] == ("work", "create"):
            identity = self.arg(args, "--work-id")
            route = next(x for x in self.routes if x["model_id"] == self.arg(args, "--launch-profile"))
            self.works[identity] = {"id": identity, "state": "ready", "created_at": "2026-01-01T00:00:00Z",
                "run_ids": [], "artifact_ids": [], "run_attempt_ids": [], "execution_selection": {
                    "launch_profile_id": route["model_id"], "launch_profile_revision": "sha256:fixture",
                    "harness": route["harness"], "provider_route": route["provider_route"],
                    "requested_model_id": route["model_id"], "revision": 1, "selected_by": review.ACTOR}}
        elif args[:2] == ("work", "claim"):
            self.works[identity]["state"] = "claimed"
        elif args[:2] == ("work", "advance"):
            work = self.works[identity]
            if "--execute" in args:
                assert self.arg(args, "--permission-mode") == "plan"
                selected = work["execution_selection"]
                rid = "ratt_fixture_" + str(len(self.works))
                snap = {k: v for k, v in selected.items() if k not in {"revision", "selected_by"}}
                snap["selection_revision"] = selected["revision"]
                attempt = {"work_id": identity, "status": "succeeded", "harness": selected["harness"],
                    "actor": selected["harness"], "launch_profile_id": selected["launch_profile_id"],
                    "requested_model_id": selected["requested_model_id"], "requested_provider_route": selected["provider_route"],
                    "execution_selection": snap, "execution_posture": {"permission_mode": "plan", "policy_decision_id": "pol_fixture"},
                    "action_intent_id": "act_fixture"}
                work["run_ids"].append("act_fixture")
                work["run_attempt_ids"].append(rid)
                self.put(self.home / "run_attempts" / (rid + ".json"), attempt)
                response = copy.deepcopy(self.response)
                if self.invalid_quote:
                    response["claims"][0]["source_quote"] = "invented source text that was never in the source"
                (self.home / "run_attempts" / (rid + ".transcript")).write_text("stdout: " + json.dumps(response) + "\n")
                work["state"] = "verifying"
            elif "--artifact" in args:
                work["artifact_ids"].append(self.arg(args, "--artifact"))
        elif args[:2] == ("work", "close"):
            self.works[identity]["state"] = self.arg(args, "--outcome")
        else:
            raise AssertionError(args)
        return ""

    def json(self, *args):
        self.calls.append(args)
        if args[:2] == ("work", "show"):
            if args[2] not in self.works:
                raise review.intake.TickError("not found: " + args[2] + ".json: no such file or directory")
            return copy.deepcopy(self.works[args[2]])
        if args[:2] == ("harness", "executors"):
            return {"executors": ["claude_minimax", "claude_qwen"]}
        if args[:2] == ("route", "plan"):
            assert "--read-only" in args
            return {"read_only": True, "chain": self.routes}
        if args[:2] == ("work", "act"):
            action = json.loads(Path(args[-1]).read_text())
            work = self.works[args[2]]
            if action["id"] in work["run_ids"]:
                raise review.intake.TickError("action already recorded")
            if self.allow:
                work["run_ids"].append(action["id"])
            return {"action_intent_id": action["id"], "outcome": "allow" if self.allow else "hold"}
        if args[:2] == ("finding", "record"):
            if self.fail_record:
                raise review.intake.TickError("custody interrupted")
            body = Path(self.arg(args, "--body-file")).read_text()
            fid = "find_" + review.scout.sha(body.encode())[7:19]
            self.put(self.home / "observed/findings" / (fid + ".json"), {"id": fid, "cid": review.scout.sha(body.encode()), "body": body})
            return {"id": fid}
        raise AssertionError(args)

    def launches(self):
        return [c for c in self.calls if "--execute" in c]


class ReviewTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.output = self.root / "staging"
        (self.output / "ticks").mkdir(parents=True)
        self.now = dt.datetime.now(dt.timezone.utc)
        self.obs = {"id": "srcobs_" + "a" * 24, "revision": "sha256:revision",
            "observed_at": self.now.isoformat(), "source": {"existing_compound_owner": "quota-owner", "frontier": "scoped quota evidence"},
            "bodies": {"find_abcdef": "The provider returns the applicable model scope on each limit."}}
        self.cli = GovernedFixture(self.root, self.obs)

    def tick(self, dry=False, observations=None):
        with patch.object(review, "observations", return_value=observations or [self.obs]):
            return review.tick(self.cli, {}, self.output, self.cli.home, self.root, self.now, dry)

    def test_audit_challenge_and_replay_use_two_independent_runs(self):
        self.assertEqual(self.tick()["role"], "audit")
        self.assertEqual(len(self.cli.launches()), 1)
        self.assertEqual(self.tick()["role"], "challenge")
        self.assertEqual(len(self.cli.launches()), 2)
        providers = [w["execution_selection"]["provider_route"] for w in self.cli.works.values()]
        self.assertEqual(providers, ["minimax", "qwen"])
        self.assertEqual(self.tick()["status"], "idle")
        self.assertEqual(len(self.cli.launches()), 2)

    def test_custody_interruption_recovers_without_relaunch(self):
        self.cli.fail_record = True
        with self.assertRaisesRegex(review.intake.TickError, "custody interrupted"):
            self.tick()
        self.assertEqual(next(iter(self.cli.works.values()))["state"], "verifying")
        self.cli.fail_record = False
        self.assertEqual(self.tick()["status"], "reviewed")
        self.assertEqual(len(self.cli.launches()), 1)

    def test_policy_hold_preserves_verifying_and_prevents_custody(self):
        self.cli.allow = False
        with self.assertRaisesRegex(review.intake.TickError, "policy did not allow"):
            self.tick()
        self.assertFalse(any(c[:2] == ("finding", "record") for c in self.cli.calls))
        self.cli.allow = True
        self.assertEqual(self.tick()["status"], "reviewed")
        self.assertEqual(len(self.cli.launches()), 1)

    def test_invalid_citations_exhaust_bounded_attempts(self):
        self.cli.invalid_quote = True
        for _ in range(3):
            with self.assertRaises(review.ReviewRejected):
                self.tick()
        result = self.tick()
        self.assertEqual(result["waiting"][0]["reason"], "attempts_exhausted")
        self.assertEqual(len(self.cli.launches()), 3)
        self.assertFalse(any(c[:2] == ("finding", "record") for c in self.cli.calls))

    def test_failed_job_prefers_another_eligible_tuple(self):
        self.cli.invalid_quote = True
        with self.assertRaises(review.ReviewRejected):
            self.tick()
        self.cli.invalid_quote = False
        self.assertEqual(self.tick()["status"], "reviewed")
        providers = [w["execution_selection"]["provider_route"] for w in self.cli.works.values()]
        self.assertEqual(providers, ["minimax", "qwen"])

    def test_empty_audit_does_not_spend_an_independent_challenge(self):
        self.cli.response["claims"] = []
        self.tick()
        self.assertEqual(self.tick()["status"], "idle")
        self.assertEqual(len(self.cli.launches()), 1)

    def test_waits_for_independent_capacity(self):
        self.cli.routes = [lane("minimax")]
        self.tick()
        result = self.tick()
        self.assertEqual(result["waiting"][0]["reason"], "no_qualified_independent_capacity")
        self.assertEqual(len(self.cli.launches()), 1)

    def test_unknown_unregistered_or_ineligible_routes_do_not_launch(self):
        for field, value in [("harness", "qwen_code"), ("gate_role", "judge_only"), ("capacity", {"state": "unknown"})]:
            with self.subTest(field=field):
                self.cli.routes = [dict(lane("minimax"), **{field: value})]
                self.assertEqual(self.tick()["status"], "idle")
                self.assertFalse(self.cli.works)

    def test_dry_run_has_no_work_or_findings(self):
        before = sorted(self.root.rglob("*"))
        self.assertEqual(self.tick(True)["status"], "would_review")
        self.assertFalse(self.cli.works)
        self.assertEqual(sorted(self.root.rglob("*")), before)

    def test_old_planner_refused_before_planning_or_work(self):
        with patch.object(self.cli, "call", return_value="old route help"):
            with self.assertRaisesRegex(review.intake.TickError, "lacks read-only"):
                self.tick()
        self.assertFalse(any(c[:2] == ("route", "plan") for c in self.cli.calls))
        self.assertFalse(self.cli.works)

    def test_delivered_observation_requires_owned_pinned_bodies(self):
        with patch.multiple(review.scout, GITHUB_WEB_HOST="github.example", GITHUB_API_HOST="api.github.example", GITHUB_RAW_HOST="raw.github.example"):
            config = cfg()
            staged = review.scout.run(config, str(self.output), review.scout.Fetch(Fake().get), now=self.now.isoformat())
            identity = staged["staged"][0]
            path = self.output / (identity + ".json")
            obs = json.loads(path.read_text())
            digest = next(r.removeprefix("pending_body:") for r in obs["evidence_refs"] if r.startswith("pending_body:"))
            body = (self.output / "content/sha256" / (digest[7:] + ".txt")).read_text()
            record_path = self.cli.home / "observed/findings/find_abcdef.json"
            self.cli.put(record_path, {"id": "find_abcdef", "cid": digest, "body": body})
            delivered = copy.deepcopy(obs)
            delivered["evidence_refs"] = ["finding:find_abcdef"]
            self.cli.put(self.output / "ticks" / (identity + ".intake.json"), delivered)
            review.scout.run(config, str(self.output), ack=identity)
            self.assertEqual(len(review.observations(config, self.output, self.cli.home, self.now)), 1)
            self.assertEqual(review.observations(config, self.output, self.cli.home, self.now + dt.timedelta(days=15)), [])
            self.cli.put(record_path, {"id": "find_abcdef", "cid": digest, "body": "tampered"})
            with self.assertRaisesRegex(review.intake.TickError, "content hash"):
                review.observations(config, self.output, self.cli.home, self.now)

    def test_in_progress_work_never_launches_a_duplicate(self):
        identity = review.work_id(self.obs, "audit", 1)
        self.cli.works[identity] = {"id": identity, "state": "running"}
        self.assertEqual(self.tick()["waiting"][0]["reason"], "in_progress")
        self.assertFalse(self.cli.launches())

    def test_actual_executor_drift_refuses_recovery(self):
        self.cli.fail_record = True
        with self.assertRaises(review.intake.TickError):
            self.tick()
        self.cli.fail_record = False
        path = next((self.cli.home / "run_attempts").glob("*.json"))
        original = json.loads(path.read_text())
        for field in ("actor", "harness", "launch_profile_id", "requested_model_id", "requested_provider_route", "action_intent_id"):
            with self.subTest(field=field):
                self.cli.put(path, dict(original, **{field: "foreign"}))
                self.assertEqual(self.tick()["waiting"][0]["reason"], "finalization_wait")
        self.assertEqual(len(self.cli.launches()), 1)

    def test_completed_report_revision_cannot_be_reused(self):
        self.tick()
        self.obs["revision"] = "sha256:changed"
        result = self.tick()
        self.assertEqual(result["waiting"][0]["reason"], "completed_evidence_unavailable")
        self.assertIn("revision", result["waiting"][0]["error"])

    def test_missing_transcript_does_not_starve_other_observations(self):
        self.cli.fail_record = True
        with self.assertRaises(review.intake.TickError):
            self.tick()
        self.cli.fail_record = False
        next((self.cli.home / "run_attempts").glob("*.transcript")).unlink()
        other = dict(self.obs, id="srcobs_" + "b" * 24)
        result = self.tick(observations=[self.obs, other])
        self.assertEqual(result["status"], "reviewed")
        self.assertEqual(result["waiting"][0]["reason"], "finalization_wait")
        self.assertEqual(len(self.cli.launches()), 2)
        first = self.cli.works[review.work_id(self.obs, "audit", 1)]
        self.assertEqual(first["state"], "verifying")

    def test_unsuccessful_attempt_cannot_be_accepted_on_recovery(self):
        self.cli.fail_record = True
        with self.assertRaises(review.intake.TickError):
            self.tick()
        self.cli.fail_record = False
        path = next((self.cli.home / "run_attempts").glob("*.json"))
        record = json.loads(path.read_text())
        record["status"] = "failed"
        self.cli.put(path, record)
        result = self.tick()
        self.assertEqual(result["waiting"][0]["reason"], "finalization_wait")
        self.assertIs(result["finalization_attempted"], True)
        self.assertNotIn("mutations", result)
        self.assertEqual(len(self.cli.launches()), 1)

    def test_empty_response_is_rejected_and_retry_is_bounded(self):
        self.cli.fail_record = True
        with self.assertRaises(review.intake.TickError):
            self.tick()
        self.cli.fail_record = False
        next((self.cli.home / "run_attempts").glob("*.transcript")).write_text("stderr: no response\n")
        self.assertEqual(self.tick()["waiting"][0]["reason"], "finalization_wait")
        self.assertEqual(next(iter(self.cli.works.values()))["state"], "failed")
        self.assertEqual(self.tick()["status"], "reviewed")
        self.assertEqual(len(self.cli.launches()), 2)

    def test_execution_error_keeps_governed_state_without_fake_finalization(self):
        original = self.cli.call
        def failure(*args):
            if "--execute" in args:
                self.cli.works[args[2]]["state"] = "running"
                raise review.intake.TickError("interrupted execution")
            return original(*args)
        with patch.object(self.cli, "call", side_effect=failure):
            with self.assertRaisesRegex(review.intake.TickError, "interrupted"):
                self.tick()
        self.assertEqual(self.tick()["waiting"][0]["reason"], "in_progress")
        self.assertFalse(any(c[:2] == ("work", "close") for c in self.cli.calls))

    def test_non_absent_inventory_error_never_creates_new_work(self):
        with patch.object(self.cli, "json", side_effect=review.intake.TickError("work store corrupt")):
            with self.assertRaisesRegex(review.intake.TickError, "store corrupt"):
                self.tick()
        self.assertFalse(self.cli.works)

    def test_ranking_does_not_turn_opinions_into_adoption(self):
        claim = self.cli.response["claims"][0]
        for change, expected in [({"novelty": "duplicates"}, "park"), ({"novelty": "unknown"}, "needs-evidence"), ({"verdict": "contradicted"}, "disputed"), ({}, "needs-experiment")]:
            self.assertEqual(review.priority_band("challenge", [dict(claim, **change)]), expected)
        self.tick()
        record = next((self.cli.home / "observed/findings").glob("*.json"))
        report = json.loads(json.loads(record.read_text())["body"])
        self.assertIs(report["adoption_allowed"], False)
        self.assertEqual(report["priority_band"], "needs-independent-review")


if __name__ == "__main__":
    unittest.main()
