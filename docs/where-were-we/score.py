#!/usr/bin/env python3
"""Conservative automatic checks for the five fixed "Where were we?" facts.

  score.py FILE...    print automatic credits and missing facts for each answer
  score.py -          read an answer from standard input

These bounded English patterns are a convenience heuristic, not semantic grading
or the source of the manually read scores retained in results.json. Credit needs
an explicit assertion in a supported form. Conditional/uncertain sentences earn
no credit; a recognized contradiction anywhere vetoes that fact, in either order.
Unrecognized paraphrases, quotations and complex negation still need human review.
"""
import re
import sys


def _has(pattern, text):
    return bool(re.search(pattern, text))


def _sentences(answer):
    # Keep version numbers and hello.py intact; remove common Markdown decoration.
    clean = re.sub(r"[\x60*_]", "", (answer or "").lower().replace("’", "'"))
    return [s.strip() for s in re.split(r"(?<=[.!?])\s+|\n|;", clean) if s.strip()]


def _denied(sentence):
    return _has(r"\b(?:not true|false|incorrect|untrue)\b", sentence)


def _asserted(sentence):
    # "Until you choose" states a present hold; "if you choose" is hypothetical.
    return not _denied(sentence) and not _has(
        r"\b(if|unless|when|once|provided|assuming|suppos\w*|hypothetical\w*|maybe|perhaps|might|may|could|would)\b"
        r"|\b(?:not sure|unclear|unknown|don't know|do not know|cannot confirm|can't confirm)\b"
        r"|\?$", sentence)


JSON = r"(?:--)?json(?:\s+(?:flag|option|support))?"
NAME = r"(?:\b(?:version|release|1\.0)\b[^?!\n]{0,45}\bnam(?:e|ing)\b|\bnam(?:e|ing)\b[^?!\n]{0,45}(?:version|release|1\.0)\b)"
TAG = r"(?:tag(?:s|ged|ging)?|releas(?:e|es|ed|ing))"
HOLD = r"(?:on hold|held|blocked|waiting|waits|paused|deferred)"


def _flag_yes(s):
    return _has(
        JSON + r"[^!?\n]{0,60}\b(?:is|was|has been)\s+(?:done|finished|implemented|added|completed)\b"
        r"|\b(?:finished|completed|done)\b[^!?\n]{0,45}" + JSON
        + r"|\b(?:added|implemented|shipped)\s+(?:(?:a|the)\s+)?" + JSON
        + r"|\b(?:has|supports)\s+(?:the\s+)?" + JSON
        + r"|\b(?:commit|commits|latest being)\b[^!?\n]{0,65}\badd\s+(?:a\s+)?" + JSON
        + r"|\bhas the initial script plus the\s+" + JSON
        + r"|\blast recorded work\b[^!?\n]{0,30}\badding\s+" + JSON, s)


def _flag_no(s):
    return _has(
        r"\b(?:no|without)\s+" + JSON
        + r"|" + JSON + r"[^!?\n]{0,45}\b(?:unfinished|incomplete|missing|absent|pending|broken|never\s+(?:implemented|added|finished)|not\s+(?:done|finished|implemented|added|complete))\b"
        r"|\b(?:didn't|did not|haven't|have not|never|not)\s+(?:yet\s+)?(?:add|finish|implement|complete)(?:ed|ing)?\b[^!?\n]{0,35}" + JSON
        + r"|\b(?:add|implement|finish)\s+(?:the\s+)?" + JSON + r"\s+(?:next|now)\b", s)


def _color_yes(s):
    return _has(
        r"\b(?:no|without)\s+colou?red?\s+output\b"
        r"|\bcolou?red?\s+output\s+(?:is|was|remains)\s+(?:ruled out|rejected|excluded|forbidden|decided against)\b"
        r"|\b(?:decided|decision)\s+against\s+colou?red?\s+output\b"
        r"|\b(?:output\s+(?:stays|remains|is)\s+)?(?:uncolou?red|colou?r-free)\b"
        r"|\bcolou?red?\s+output:\s+[^!?\n]{0,30}(?:decided against it|ruled it out|won't get any)\b"
        r"|\bstays\s+free of\s+colou?r\b"
        r"|\bcolou?red?\s+output\s+stays\s+off the table\b"
        r"|\bno\s+colou?rs\b"
        r"|\bcolou?r\s+is\s+out\b"
        r"|\b(?:will not|won't|do not|don't)\s+add\s+colou?red?\s+output\b", s)


def _color_no(s):
    s = re.sub(r"\b(?:not|never|no|won't|don't)\s+(?:to\s+)?(?:add|enable|use|want|allow|include)\s+colou?red?\s+output", "", s)
    return _has(
        r"\b(?:add|enable|use|want|allow|include)(?:s|d|ing)?\s+colou?red?\s+output\b"
        r"|\bcolou?red?\s+output\s+(?:is|was|remains)\s+(?:enabled|allowed|wanted|required)\b"
        r"|\b(?:not|never)\s+(?:ruled out|rejected|uncolou?red|colou?r-free)\b"
        r"|\b(?:did not|didn't|never)\s+decide\s+against\s+colou?red?\s+output\b"
        r"|\bcolou?r\s+is\s+not\s+out\b", s)


def _reason_yes(s):
    return _has(
        r"\b(?:output|script)\b[^!?\n]{0,45}\b(?:is|gets|got|was)\s+piped\s+(?:in)?to\s+(?:other\s+)?(?:tools|programs)\b"
        r"|\b(?:output|script)\b[^!?\n]{0,30}\bfeeds\s+(?:other|downstream)\s+(?:tools|programs)\b"
        r"|\bother tools\s+consume\s+[^!?\n]{0,25}\boutput\b"
        r"|\b(?:output|uncolou?red|colou?r-free)\b[^!?\n]{0,35}\bfor\s+piping\b", s)


def _reason_no(s):
    return _has(
        r"\b(?:output|script)\b[^!?\n]{0,45}\b(?:not|never|isn't|is not|doesn't|does not|won't|will not)\b[^!?\n]{0,25}\b(?:pip\w*|feed\w*|downstream|other tools)\b"
        r"|\b(?:not|never)\s+(?:because|for)\b[^!?\n]{0,45}\b(?:pip\w*|tools|programs)\b"
        r"|\b(?:output|script)\b[^!?\n]{0,35}\bonly\s+(?:for|read by)\s+(?:people|humans)\b", s)


def _name_yes(s):
    return _has(NAME, s) and _has(
        r"\b(?:waiting|waits|pending|still open|remaining|open item|on hold|blocked|unresolved|undecided|not (?:yet )?(?:chosen|decided|picked))\b"
        r"|\b(?:you\s+)?(?:pick|choose)\s+[^!?\n]{0,20}" + NAME
        + r"|\b(?:still need|still needed|need|needs)\s+to\s+(?:pick|choose|decide)\b", s)


def _name_no(s):
    return _has(NAME, s) and _has(
        r"\b(?:already\s+(?:chosen|picked|decided|settled)|(?:is|was|has been)\s+(?:chosen|picked|decided|settled))\b"
        r"|\b(?:no longer|not)\s+(?:waiting|pending|blocked)\b"
        r"|\b(?:do not|don't)\s+need\s+to\s+(?:pick|choose|decide)\b", s)


def _hold_yes(s):
    return _has(
        r"\bno\s+(?:tag(?:ging)?|releas(?:e|ing))\b"
        r"|\b(?:not|never|don't|do not|won't|will not)\s+" + TAG
        + r"|\bnothing\s+(?:gets|is)\s+" + TAG + r"[^!?\n]{0,55}\b(?:until|before)\b"
        r"|\bno\s+tags?[^!?\n]{0,45}\buntil\b"
        r"|\b" + TAG + r"[^!?\n]{0,65}\b" + HOLD
        + r"|\b" + HOLD + r"[^!?\n]{0,45}\b" + TAG
        + r"|\b" + TAG + r"[^!?\n]{0,30}\bneeds?\s+your\s+explicit\s+go-ahead\b", s)


def _hold_no(s):
    s = re.sub(r"\b(?:not|never|don't|do not|won't|will not)\s+" + TAG, "", s)
    return _has(
        r"\b" + TAG + r"[^!?\n]{0,40}\b(?:now|immediately|today|is allowed|are allowed|is authorized|are authorized)\b"
        r"|\b(?:will|should|can|must)\s+" + TAG
        + r"|\b(?:go ahead|proceed)\b[^!?\n]{0,35}\b" + TAG
        + r"|\b" + TAG + r"[^!?\n]{0,35}\b(?:no longer|not)\s+(?:on hold|held|blocked|waiting)\b"
        r"|\b(?:not|never)\s+(?:hold|pause|block)\b[^!?\n]{0,30}\b" + TAG, s)


# A positive and a global veto belong to each fixed fact. Vetoes deliberately run
# over ALL sentences, including uncertain ones; no first-match or last-claim wins.
_RULES = [
    ("the flag is done", _flag_yes, _flag_no),
    ("no colored output", _color_yes, _color_no),
    ("the reason: the output goes to other tools", _reason_yes, _reason_no),
    ("the release waits on the version name", _name_yes, _name_no),
    ("nothing gets tagged or released", _hold_yes, _hold_no),
]
FACTS = [(name, positive) for name, positive, _ in _RULES]


def score(answer):
    """Return automatic credits, requiring a supported assertion and no veto."""
    sentences = _sentences(answer)
    return [name for name, positive, negative in _RULES
            if not any(negative(s) or (_denied(s) and positive(s)) for s in sentences)
            and any(_asserted(s) and positive(s) for s in sentences)]


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
