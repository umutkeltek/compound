#!/usr/bin/env python3
"""Score an answer to "Where were we?" against the five facts of the test.

  score.py FILE...        one line per file: how many of the five facts the answer states, and which are missing
  score.py -              read one answer from standard input

A fact counts only when the answer states it. Wording varies ("piped", "for piping", "feeds other tools",
"release name", "version name"), so each rule lists the wordings seen in the first 48 runs. No rule matches
an answer that only describes the state of the repository ("no stashes, no other branches, no tags").
"""
import re
import sys


def _hold(a):
    """Tagging or releasing is on hold: a tag verb, or "tags"/"release" next to a hold word, in one sentence."""
    for sent in re.split(r"(?<=[.!?])\s+|\n", a):
        if re.search(r"\btagg?(ed|ing)\b|\btag\b", sent):
            return True
        if re.search(r"releas", sent) and re.search(r"\btags\b", sent):
            return True
        if re.search(r"releas(e|es|ed|ing)\b", sent) and re.search(
                r"on hold|\bhold\b|\bheld\b|blocked|until|waiting|waits|not releas|no releas|n't releas", sent):
            return True
    return False


FACTS = [
    ("the flag is done", lambda a: "json" in a),
    ("no colored output", lambda a: bool(re.search(r"colou?r", a))),
    ("the reason: the output goes to other tools",
     lambda a: bool(re.search(r"\bpip(e|ed|es|ing)\b|other tools|other programs|downstream", a))),
    ("the release waits on the version name",
     lambda a: bool(re.search(r"(version|release|1\.0)[^.\n]{0,40}\bnam(e|ing)\b|\bnam(e|ing)\b[^.\n]{0,40}(version|release|1\.0)", a))),
    ("nothing gets tagged or released", _hold),
]


def score(answer):
    """The names of the facts the answer states."""
    low = (answer or "").lower()
    return [name for name, test in FACTS if test(low)]


def main(argv):
    if not argv:
        raise SystemExit(__doc__)
    for path in argv:
        text = sys.stdin.read() if path == "-" else open(path, errors="replace").read()
        got = score(text)
        missing = [name for name, _ in FACTS if name not in got]
        print("%d of %d  %s%s" % (len(got), len(FACTS), path, ("  missing: " + "; ".join(missing)) if missing else ""))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
