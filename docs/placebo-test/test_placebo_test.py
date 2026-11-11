"""Tests for the scoring rules of placebo_test.py. Run: python3 -m unittest test_placebo_test (in this folder)."""
import json
import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import placebo_test as pt  # noqa: E402

RULE = {"win_bar": 6, "token_ratio_cap": 2.0}


def cfg(**over):
    base = {"claude_bin": "claude", "model": "opus", "off_args": list(pt.DEFAULT_OFF_ARGS), "on_args": [],
            "marker": "", "deny": [], "arm_timeout": 480, "judge_cmd": ["judge"], "judge_timeout": 300}
    base.update(over)
    return base


def facts(**over):
    base = pt.parse_stream("")
    base["answer"] = "an answer"
    base.update(over)
    return base


def trial_file(folder, rule, n=3):
    path = os.path.join(folder, "tasks.json")
    tasks = [{"id": "T%02d" % i, "cwd": folder, "task": "do %d" % i, "known_right_action": "right %d" % i}
             for i in range(1, n + 1)]
    with open(path, "w") as fh:
        json.dump({"rule": rule, "tasks": tasks}, fh)
    return path


class Verdicts(unittest.TestCase):
    def test_last_line_token(self):
        self.assertEqual(pt.parse_verdict("reasoning...\n2"), "2")
        self.assertEqual(pt.parse_verdict("Verdict: tie\n"), "TIE")
        self.assertEqual(pt.parse_verdict("**1**"), "1")

    def test_no_verdict_line_is_none(self):
        self.assertIsNone(pt.parse_verdict("both differ only in cosmetic ways"))
        self.assertIsNone(pt.parse_verdict(""))
        self.assertIsNone(pt.parse_verdict("1\nbut then I changed my mind"))

    def test_win_needs_the_same_pick_in_both_orders(self):
        self.assertEqual(pt.combine("2", "1"), "on")    # answer 2 is ON when OFF is shown first
        self.assertEqual(pt.combine("1", "2"), "off")

    def test_order_flip_is_a_tie(self):
        self.assertEqual(pt.combine("1", "1"), "tie")   # the judge picked whatever came first
        self.assertEqual(pt.combine("2", "2"), "tie")   # the judge picked whatever came second

    def test_a_tie_in_either_order_is_a_tie(self):
        self.assertEqual(pt.combine("TIE", "1"), "tie")
        self.assertEqual(pt.combine("TIE", "TIE"), "tie")


class Rule(unittest.TestCase):
    def test_first_run_score_does_not_keep(self):
        # The first recorded run: 3 wins each, 4 ties, ON used fewer tokens. The bar was 6.
        pairs = ["tie", "off", "off", "tie", "tie", "on", "on", "tie", "on", "off"]
        r = pt.apply_rule(pairs, 4092860, 2956178, RULE, 10)
        self.assertEqual((r["on_wins"], r["off_wins"], r["ties"]), (3, 3, 4))
        self.assertEqual(r["token_ratio_on_over_off"], 0.722)
        self.assertEqual(r["outcome"], "NOT-KEPT")

    def test_bar_is_outright_wins_not_a_share_of_decided_pairs(self):
        pairs = ["on"] * 5 + ["tie"] * 5      # 100% of decided pairs, still under the bar
        self.assertEqual(pt.apply_rule(pairs, 100, 100, RULE, 10)["outcome"], "NOT-KEPT")
        pairs = ["on"] * 6 + ["off"] * 4
        self.assertEqual(pt.apply_rule(pairs, 100, 100, RULE, 10)["outcome"], "KEEP")

    def test_token_cap_can_refuse_a_winner(self):
        pairs = ["on"] * 8 + ["tie"] * 2
        self.assertEqual(pt.apply_rule(pairs, 100, 201, RULE, 10)["outcome"], "NOT-KEPT")
        self.assertEqual(pt.apply_rule(pairs, 100, 200, RULE, 10)["outcome"], "KEEP")
        no_cap = {"win_bar": 6, "token_ratio_cap": None}
        self.assertEqual(pt.apply_rule(pairs, 100, 900, no_cap, 10)["outcome"], "KEEP")

    def test_a_pilot_is_never_a_verdict(self):
        r = pt.apply_rule(["on", "on"], 100, 100, {"win_bar": 2, "token_ratio_cap": None}, 10)
        self.assertFalse(r["complete"])
        self.assertEqual(r["outcome"], "INCOMPLETE")

    def test_an_unanswered_pair_is_not_a_tie(self):
        pairs = ["on"] * 6 + ["tie"] * 3 + ["unjudged"]
        r = pt.apply_rule(pairs, 100, 100, RULE, 10)
        self.assertEqual((r["ties"], r["unjudged"]), (3, 1))
        self.assertEqual(r["outcome"], "INCOMPLETE")   # six wins, and still no verdict until it is judged


class FirstRun(unittest.TestCase):
    def test_published_outcome_follows_from_the_published_verdicts(self):
        here = os.path.dirname(os.path.abspath(__file__))
        with open(os.path.join(here, "results-2026-11-03.json")) as fh:
            doc = json.load(fh)
        pairs = [pt.combine(t["verdict_off_first"], t["verdict_on_first"]) for t in doc["tasks"]]
        self.assertEqual(pairs, [t["winner"] for t in doc["tasks"]])
        tok_off = sum(t["off"]["tokens_total"] for t in doc["tasks"])
        tok_on = sum(t["on"]["tokens_total"] for t in doc["tasks"])
        self.assertEqual(pt.apply_rule(pairs, tok_off, tok_on, RULE, 10), doc["rule"])


class TrialFileAndLock(unittest.TestCase):
    def test_rule_must_be_written_first(self):
        with tempfile.TemporaryDirectory() as d:
            for bad in ({}, {"win_bar": 0}, {"win_bar": 4}, {"win_bar": "6"}, {"win_bar": True}):
                with self.assertRaises(SystemExit):
                    pt.load_trial(trial_file(d, bad))
            rule, ids, tasks = pt.load_trial(trial_file(d, {"win_bar": 2}))
            self.assertEqual(rule, {"win_bar": 2, "token_ratio_cap": None})
            self.assertEqual(ids, ["T01", "T02", "T03"])
            self.assertEqual(len(pt.load_trial(trial_file(d, {"win_bar": 2}), "T02")[2]), 1)

    def test_changed_rule_is_refused_after_the_trial_started(self):
        with tempfile.TemporaryDirectory() as d:
            out = os.path.join(d, "out")
            pt.lock_rule(out, {"win_bar": 6, "token_ratio_cap": 2.0}, ["T01", "T02"])
            pt.lock_rule(out, {"win_bar": 6, "token_ratio_cap": 2.0}, ["T01", "T02"])  # unchanged: fine
            with self.assertRaises(SystemExit):
                pt.lock_rule(out, {"win_bar": 3, "token_ratio_cap": 2.0}, ["T01", "T02"])  # lowered bar
            with self.assertRaises(SystemExit):
                pt.lock_rule(out, {"win_bar": 6, "token_ratio_cap": 2.0}, ["T01"])  # dropped a task

    def test_dry_run_writes_no_lock(self):
        with tempfile.TemporaryDirectory() as d:
            out = os.path.join(d, "out")
            pt.lock_rule(out, RULE, ["T01"], write=False)
            self.assertFalse(os.path.exists(os.path.join(out, "rule.json")))


class Trial(unittest.TestCase):
    """The steps against a real folder, with a stand-in judge and no model calls."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.out = os.path.join(self.tmp.name, "out")
        os.makedirs(os.path.join(self.out, "raw"))
        self.tasks = [{"id": "T01", "cwd": self.tmp.name, "task": "q", "known_right_action": "k"}]
        for arm in pt.ARMS:
            base = os.path.join(self.out, "raw", "T01.%s" % arm)
            with open(base + ".answer.md", "w") as fh:
                fh.write("answer from %s" % arm)
            with open(base + ".meta.json", "w") as fh:
                json.dump({"tokens_total": 10, "tokens_output": 1, "wall_s": 1.0, "num_turns": 1,
                           "invalid": [], "mutation_flags": []}, fh)

    def judge(self, script):
        return cfg(judge_cmd=[sys.executable, "-c", script], judge_timeout=20)

    def score(self):
        return pt.cmd_score(self.tasks, self.out, {"win_bar": 1, "token_ratio_cap": None}, 1)["rule"]

    def test_a_silent_judge_gives_no_verdict(self):
        pt.cmd_judge(self.judge("print('I cannot decide')"), self.tasks, self.out)
        r = self.score()
        self.assertEqual((r["unjudged"], r["ties"], r["outcome"]), (1, 0, "INCOMPLETE"))

    def test_judge_fills_only_what_is_missing_and_never_rerolls(self):
        pt.cmd_judge(self.judge("print('I cannot decide')"), self.tasks, self.out)
        # A judge that always prefers the ON answer, wherever it is shown.
        prefers_on = ("import sys; p = sys.argv[1]; "
                      "print('2' if p.index('answer from on') > p.index('answer from off') else '1')")
        pt.cmd_judge(self.judge(prefers_on), self.tasks, self.out)
        self.assertEqual(self.score()["outcome"], "KEEP")
        pt.cmd_judge(self.judge("print('TIE')"), self.tasks, self.out)   # a second opinion changes nothing
        r = self.score()
        self.assertEqual((r["on_wins"], r["ties"], r["outcome"]), (1, 0, "KEEP"))

    def test_run_keeps_a_valid_run(self):
        before = pt.read_answer(self.out, "T01", "on")
        pt.cmd_run(cfg(claude_bin="/nonexistent/claude"), self.tasks, self.out)   # would crash if it ran
        self.assertEqual(pt.read_answer(self.out, "T01", "on"), before)


class Runs(unittest.TestCase):
    STREAM = "\n".join([
        json.dumps({"type": "system", "subtype": "hook_response", "hook_event": "SessionStart",
                    "output": "MEMORY-LAYER v3 loaded"}),
        json.dumps({"type": "assistant", "message": {"content": [
            {"type": "tool_use", "name": "Bash", "input": {"command": "git log -1 > notes.txt"}},
            {"type": "tool_use", "name": "Read", "input": {"file_path": "README.md"}}]}}),
        json.dumps({"type": "result", "result": "the answer\nNEXT ACTION: read it", "num_turns": 3,
                    "usage": {"input_tokens": 10, "cache_creation_input_tokens": 20,
                              "cache_read_input_tokens": 30, "output_tokens": 5}}),
    ])

    def test_stream_facts(self):
        f = pt.parse_stream(self.STREAM, "MEMORY-LAYER")
        self.assertEqual(f["tokens_total"], 65)
        self.assertEqual(f["session_start_hooks"], 1)
        self.assertTrue(f["marker_seen"])
        self.assertTrue(f["answer"].startswith("the answer"))
        self.assertEqual(len(pt.flag_mutations(f["tool_calls"])), 1)   # the redirect, not the Read

    def test_reads_and_dev_null_are_not_flagged(self):
        calls = [{"name": "Bash", "input": json.dumps({"command": "ls -la 2>/dev/null | head"})},
                 {"name": "Grep", "input": "{}"}]
        self.assertEqual(pt.flag_mutations(calls), [])
        self.assertEqual(len(pt.flag_mutations([{"name": "Write", "input": "{}"}])), 1)

    def test_off_run_with_a_session_start_hook_cannot_count(self):
        self.assertTrue(pt.validity(cfg(), "off", facts(session_start_hooks=1), False))
        self.assertEqual(pt.validity(cfg(), "on", facts(session_start_hooks=1), False), [])
        custom = cfg(off_args=["--mcp-config", "none.json"])   # a custom switch says nothing about hooks
        self.assertEqual(pt.validity(custom, "off", facts(session_start_hooks=1), False), [])

    def test_marker_proves_each_arm(self):
        c = cfg(marker="MEMORY-LAYER")
        self.assertTrue(pt.validity(c, "on", facts(marker_seen=False), False))
        self.assertTrue(pt.validity(c, "off", facts(marker_seen=True), False))
        self.assertEqual(pt.validity(c, "on", facts(marker_seen=True), False), [])
        self.assertEqual(pt.validity(c, "off", facts(marker_seen=False), False), [])

    def test_timeout_and_empty_answer_cannot_count(self):
        self.assertEqual(len(pt.validity(cfg(), "on", facts(answer=" "), True)), 2)

    def test_arms_differ_only_by_the_switch(self):
        off, on = pt.arm_command(cfg(), "off", "p"), pt.arm_command(cfg(), "on", "p")
        self.assertEqual(off[:len(on)], on)
        self.assertEqual(off[len(on):], pt.DEFAULT_OFF_ARGS)
        self.assertIn("plan", on)


if __name__ == "__main__":
    unittest.main()
