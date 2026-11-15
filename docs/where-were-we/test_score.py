"""Tests for the scoring rules. Run: python3 -m unittest test_score (in this folder).

FULL and REPO_ONLY contain real answers from the first 48 runs, shortened. The repo-only answers are the control:
they describe the repository in detail and must still score one fact.
"""
import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import score as sc  # noqa: E402

FULL = [
    # a new session reading the first agent's own notes
    "The 1.0 release is waiting on you. You still need to pick the version name. Until then, I'm not tagging, "
    "bumping versions, or releasing. The code is committed and clean: hello.py has the --json flag. "
    "Colored output is ruled out. You decided against it because the script's output is piped into other tools.",
    # the other agent reading notes in the repo
    "We'd finished adding --json to hello.py and recorded passing manual checks. We also decided against colored "
    "output because the script feeds other tools. Still open: you pick the 1.0 release name. Tagging and releasing "
    "remain on hold.",
    # the other agent reading the recorded session
    "We'd finished adding --json to hello.py. Plain text remains the default; output stays uncolored for piping. "
    "The pending decision is your version name for 1.0. You'd explicitly put tagging and releasing on hold until "
    "you choose it.",
]
REPO_ONLY = [
    "I don't have a record of an earlier conversation. All I can go on is the repo. Branch: main, with a clean "
    "working tree, no stashes and no other branches. Last commit: add --json flag, which changed hello.py. "
    "Nothing is left in progress on disk.",
    "The last recorded work here was adding --json support to hello.py, so greetings can print as plain text or "
    "JSON. That's committed on main, and the working tree is clean. I couldn't recover a saved next step.",
    "The last thing done was adding a --json flag to hello.py. The working tree is clean, on main, with no other "
    "branches, tags or stashes, so nothing is left half-finished. Two loose ends: the README does not mention "
    "--json, and there are no tests.",
]


class Score(unittest.TestCase):
    def test_answers_that_state_all_five(self):
        for a in FULL:
            self.assertEqual(len(sc.score(a)), 5, a[:60])

    def test_an_answer_built_from_the_repo_alone_scores_one(self):
        for a in REPO_ONLY:
            self.assertEqual(sc.score(a), ["the flag is done"], a[:60])

    def test_a_list_of_what_the_repo_lacks_is_not_a_hold(self):
        # "no tags" describes the repository; it does not say that tagging is on hold
        self.assertNotIn("nothing gets tagged or released", sc.score("There are no tags and no stashes on main."))
        self.assertIn("nothing gets tagged or released", sc.score("No tags, releases, or version bumps until then."))

    def test_knowing_the_decision_without_the_reason_scores_four(self):
        a = ("We'd finished the --json option. Output stays color-free. Remaining: let you choose the 1.0 version "
             "name. Tagging and releasing remain on hold until you explicitly authorize them.")
        got = sc.score(a)
        self.assertEqual(len(got), 4)
        self.assertNotIn("the reason: the output goes to other tools", got)

    def test_each_fact_is_scored_on_its_own(self):
        self.assertEqual(sc.score("Nothing here."), [])
        self.assertEqual(sc.score("Colour is out."), ["no colored output"])
        self.assertEqual(sc.score("You still need to pick the version name."), ["the release waits on the version name"])


# Synthetic controls exercise supported assertions, their opposites and hypotheticals.
CONTROLS = [
    ("the flag is done",
     "The JSON flag is done.",
     ["The JSON flag is unfinished.", "The JSON flag was never implemented.",
      "We did not add the JSON flag.", "The script has no JSON flag.",
      "The JSON flag is not done.", "Finish the JSON flag now."]),
    ("no colored output",
     "No colored output.",
     ["Add colored output.", "Colored output is enabled.",
      "We did not decide against colored output.", "Colour is not out."]),
    ("the reason: the output goes to other tools",
     "The output is piped into other tools.",
     ["The output is not piped into other tools.",
      "The script does not feed downstream tools.", "The output is only for humans.",
      "Output stays uncolored, not for piping."]),
    ("the release waits on the version name",
     "You still need to pick the version name.",
     ["The version name is already decided.", "The release name is chosen.",
      "The version name is not pending.", "You don't need to pick the version name."]),
    ("nothing gets tagged or released",
     "Tagging and releasing remain on hold.",
     ["Tag and release now.", "We will tag and release.", "Tagging is no longer on hold.",
      "Releasing is allowed.", "Proceed with tagging."]),
]


class AdversarialScore(unittest.TestCase):
    def test_original_counterexample_scores_zero(self):
        self.assertEqual(sc.score(
            "The JSON flag is unfinished. Add colored output for downstream tools. "
            "The version name is already decided. Tag and release now."), [])

    def test_positive_negative_and_conditional_per_fact(self):
        for fact, positive, negatives in CONTROLS:
            with self.subTest(fact=fact, kind="positive"):
                self.assertEqual(sc.score(positive), [fact])
            for negative in negatives:
                with self.subTest(fact=fact, negative=negative):
                    self.assertNotIn(fact, sc.score(negative))
            for prefix in ("If this is true, ", "Maybe ", "Suppose ", "Perhaps ", "When this happens, ", "Assuming this, "):
                with self.subTest(fact=fact, prefix=prefix):
                    self.assertNotIn(fact, sc.score(prefix + positive))
            for wrapped in ("Is it true that " + positive.rstrip(".") + "?",
                            "It is not true that " + positive):
                with self.subTest(fact=fact, wrapped=wrapped):
                    self.assertNotIn(fact, sc.score(wrapped))

    def test_contradiction_anywhere_vetoes_in_both_orders(self):
        for fact, positive, negatives in CONTROLS:
            for negative in negatives:
                for answer in (positive + "\n\n" + negative, negative + "\n\n" + positive):
                    with self.subTest(fact=fact, answer=answer):
                        self.assertNotIn(fact, sc.score(answer))

    def test_contradiction_does_not_erase_unrelated_facts(self):
        for fact, _, negatives in CONTROLS:
            for negative in negatives:
                with self.subTest(fact=fact, negative=negative):
                    got = sc.score(FULL[0] + "\n\n" + negative)
                    self.assertNotIn(fact, got)
                    # A recognized opposite must not win by appearing after the fact.
                    self.assertEqual(got, [name for name, _ in sc.FACTS if name != fact])

    def test_keywords_without_assertions_score_zero(self):
        self.assertEqual(sc.score(
            "JSON flag. Colored output. Downstream tools. Version name. Tags and releases."), [])

    def test_unrelated_downstream_mention_is_not_the_reason(self):
        self.assertNotIn(CONTROLS[2][0], sc.score("Add colored output for downstream tools."))

    def test_mixed_polarity_in_one_sentence_is_vetoed(self):
        self.assertNotIn(CONTROLS[1][0], sc.score(
            "Do not add colored output, but enable colored output."))
        self.assertNotIn(CONTROLS[4][0], sc.score(
            "Do not tag now, but release now."))


if __name__ == "__main__":
    unittest.main()
