"""Tests for the scoring rules. Run: python3 -m unittest test_score (in this folder).

Every answer below is a real one from the first 48 runs, shortened. The repo-only answers are the control:
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


if __name__ == "__main__":
    unittest.main()
