#!/usr/bin/env python3
"""placebo_test: a blind on/off trial for one thing you load into a coding agent.

Each task runs twice in fresh, read-only Claude Code sessions: once with the thing ON and once with it OFF.
A judge from another model family then sees both answers without labels, in both orders, and picks the one
that would have led to the better action. A pick that changes with the order is a tie. A pair the judge
never answered is not a tie: the trial stays INCOMPLETE until the judge step fills it in.
The rule (how many wins the thing needs to stay) lives in the task file. It is locked when the first run
starts, and every later step refuses a rule or a task list that changed afterwards. No step redoes
finished work: a valid run and a recorded verdict are kept, so a trial can resume but cannot be re-rolled.

  placebo_test.py run   --task-file tasks.json --out DIR
  placebo_test.py judge --task-file tasks.json --out DIR --judge-cmd "opencode run -m <provider/model>"
  placebo_test.py score --task-file tasks.json --out DIR
  placebo_test.py all   --task-file tasks.json --out DIR --judge-cmd "..."     # run + judge + score
  --dry-run prints the commands and runs nothing.

OFF defaults to "all Claude Code hooks disabled", which is right for a memory that a SessionStart hook
injects. For anything else pass --off-args and --on-args: extra `claude` arguments that switch your thing
off and on. Pass --marker TEXT (a string the thing always prints into the session) and every run proves
from its own event stream which arm it was.

Outputs in DIR: rule.json, raw/<task>.<arm>.stream.jsonl, raw/<task>.<arm>.answer.md,
raw/<task>.<arm>.meta.json, judge/<task>.<order>.txt, results.json.
Model output is untrusted data here: it is parsed, never executed.
"""
import argparse
import datetime as dt
import json
import os
import re
import shlex
import subprocess
import sys
import time

ARMS = ("off", "on")
ORDERS = ("off-first", "on-first")
DEFAULT_OFF_ARGS = ["--settings", json.dumps({"disableAllHooks": True})]
DENY = ["Edit", "Write", "MultiEdit", "NotebookEdit", "Agent", "Task", "WebFetch", "WebSearch",
        "Bash(git commit:*)", "Bash(git push:*)", "Bash(git checkout:*)", "Bash(git reset:*)",
        "Bash(git merge:*)", "Bash(git rebase:*)", "Bash(rm:*)", "Bash(mv:*)", "Bash(cp:*)"]
READ_TOOLS = {"Read", "Grep", "Glob", "LS", "Skill", "ToolSearch", "TodoWrite", "ExitPlanMode", "Bash"}
MUTATING_BASH = re.compile(r"(^|[;&|]\s*)(rm|mv|cp|git\s+(commit|push|reset|checkout|merge|rebase)|tee|touch|"
                           r"mkdir|sed\s+-i)(?![\w-])|(?<![0-9&>])>{1,2}\s*(?!/dev/null)[^&\s>]")

PROMPT_TAIL = (
    "\n\nAnswer as a planning note only. Do not change anything: no edits, installs, network writes, "
    "publishing, device runs or store changes. You may read files and run read-only commands if that helps "
    "you decide. Reply in at most 200 words. End with one line that starts with \"NEXT ACTION:\" naming the "
    "exact first command, tool, skill or step you would take.")

JUDGE_TEMPLATE = """You are judging two answers to the same task. Neither author knew the outcome; you do.

TASK (both answers got exactly this):
{task}

KNOWN RIGHT ACTION (established later from the real outcome; hidden from both authors):
{known}

ANSWER 1:
<<<
{one}
>>>

ANSWER 2:
<<<
{two}
>>>

Question: which answer would have led to the better action, judged only against the known right action?
Ignore length, tone and formatting. If both are equally close or equally far, answer TIE.
Reply with exactly one token on the last line: 1, 2, or TIE."""


# ---------------------------------------------------------------- the trial file and its lock

def load_trial(path, only=None):
    """Returns (rule, every task id in the file, the selected tasks)."""
    with open(path) as fh:
        doc = json.load(fh)
    tasks = doc.get("tasks") or []
    rule = doc.get("rule") or {}
    if not tasks:
        raise SystemExit("the task file has no tasks")
    for t in tasks:
        missing = [k for k in ("id", "cwd", "task", "known_right_action") if not str(t.get(k) or "").strip()]
        if missing:
            raise SystemExit("task %s is missing: %s" % (t.get("id", "?"), ", ".join(missing)))
    ids = [t["id"] for t in tasks]
    if len(set(ids)) != len(ids):
        raise SystemExit("task ids must be unique")
    bar = rule.get("win_bar")
    if not isinstance(bar, int) or isinstance(bar, bool) or not 1 <= bar <= len(tasks):
        raise SystemExit("write the rule first: \"rule\": {\"win_bar\": N} with N between 1 and %d" % len(tasks))
    cap = rule.get("token_ratio_cap")
    if cap is not None and (isinstance(cap, bool) or not isinstance(cap, (int, float)) or cap <= 0):
        raise SystemExit("rule.token_ratio_cap must be a positive number or left out")
    rule = {"win_bar": bar, "token_ratio_cap": cap}
    selected = tasks
    if only:
        want = [x.strip() for x in only.split(",") if x.strip()]
        unknown = sorted(set(want) - set(ids))
        if unknown:
            raise SystemExit("unknown task ids: %s" % ",".join(unknown))
        selected = [t for t in tasks if t["id"] in want]
    return rule, ids, selected


def lock_rule(out_dir, rule, task_ids, write=True):
    """The rule and the task list are fixed by the first step that runs. Any later change is refused."""
    path = os.path.join(out_dir, "rule.json")
    if os.path.exists(path):
        with open(path) as fh:
            locked = json.load(fh)
        if locked.get("rule") != rule or locked.get("task_ids") != task_ids:
            raise SystemExit("the rule or the task list changed after this trial started (%s). "
                             "A changed rule needs a new --out folder and a new run." % path)
        return locked
    locked = {"rule": rule, "task_ids": task_ids,
              "locked_at": dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")}
    if write:
        os.makedirs(out_dir, exist_ok=True)
        with open(path, "w") as fh:
            json.dump(locked, fh, indent=1)
    return locked


# ---------------------------------------------------------------- commands

def arm_command(cfg, arm, prompt):
    if arm not in ARMS:
        raise ValueError(arm)
    cmd = [cfg["claude_bin"], "-p", prompt, "--model", cfg["model"], "--output-format", "stream-json",
           "--verbose", "--include-hook-events", "--permission-mode", "plan", "--no-session-persistence",
           "--disallowedTools"] + DENY + cfg["deny"]
    return cmd + (cfg["off_args"] if arm == "off" else cfg["on_args"])


def judge_command(cfg, prompt):
    return cfg["judge_cmd"] + [prompt]


def parse_stream(text, marker=""):
    """Facts from a stream-json run: final answer, usage, hook evidence, tool calls."""
    out = {"answer": "", "usage": {}, "tokens_total": 0, "tokens_output": 0, "duration_ms": None,
           "num_turns": None, "cost_usd_equiv": None, "hook_events": 0, "session_start_hooks": 0,
           "marker_seen": False, "tool_calls": [], "is_error": None, "plan_text": ""}
    for line in (text or "").splitlines():
        line = line.strip()
        if marker and marker in line:
            out["marker_seen"] = True
        if not line.startswith("{"):
            continue
        try:
            ev = json.loads(line)
        except ValueError:
            continue
        if not isinstance(ev, dict):
            continue
        typ, sub = ev.get("type"), ev.get("subtype", "")
        if typ == "system" and "hook" in str(sub):
            out["hook_events"] += 1
            if "SessionStart" in json.dumps(ev, ensure_ascii=False):
                out["session_start_hooks"] += 1
        elif typ == "assistant":
            for b in (ev.get("message") or {}).get("content") or []:
                if isinstance(b, dict) and b.get("type") == "tool_use":
                    inp = b.get("input") or {}
                    out["tool_calls"].append({"name": b.get("name"), "input": json.dumps(inp)[:300]})
                    if b.get("name") == "ExitPlanMode" and isinstance(inp, dict):
                        out["plan_text"] = str(inp.get("plan") or "")
        elif typ == "result":
            out["answer"] = ev.get("result") or ""
            u = ev.get("usage") or {}
            out["usage"] = u
            out["tokens_output"] = int(u.get("output_tokens") or 0)
            out["tokens_total"] = sum(int(u.get(k) or 0) for k in
                                      ("input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens",
                                       "output_tokens"))
            out["duration_ms"] = ev.get("duration_ms")
            out["num_turns"] = ev.get("num_turns")
            out["cost_usd_equiv"] = ev.get("total_cost_usd")
            out["is_error"] = ev.get("is_error")
    if not out["answer"].strip() and out["plan_text"]:
        out["answer"] = out["plan_text"]
    return out


def flag_mutations(tool_calls):
    """Calls that could have written something. A flag asks for a manual look; it is not proof of a write."""
    flags = []
    for c in tool_calls:
        name, inp = c.get("name") or "", c.get("input") or ""
        if name not in READ_TOOLS and not name.startswith("mcp__"):
            flags.append("non-read tool %s" % name)
        if name == "Bash":
            try:
                cmd = json.loads(inp).get("command", "") if inp.endswith("}") else inp
            except ValueError:
                cmd = inp
            if MUTATING_BASH.search(cmd or ""):
                flags.append("possibly mutating Bash: %s" % (cmd or "")[:120])
    return flags


def validity(cfg, arm, facts, timed_out):
    """Why a run cannot count. An empty list means the run is valid."""
    problems = []
    if timed_out:
        problems.append("timed out after %ds" % cfg["arm_timeout"])
    if not facts["answer"].strip():
        problems.append("empty answer")
    if arm == "off" and cfg["off_args"] == DEFAULT_OFF_ARGS and facts["session_start_hooks"]:
        problems.append("the off run shows a SessionStart hook (hooks were not off)")
    if cfg["marker"]:
        if arm == "on" and not facts["marker_seen"]:
            problems.append("the on run never shows the marker (the thing was not loaded)")
        if arm == "off" and facts["marker_seen"]:
            problems.append("the off run shows the marker (the thing was not off)")
    return problems


def parse_verdict(text):
    """The judge's last line token: '1', '2' or 'TIE'. Anything else -> None."""
    for line in reversed([ln.strip() for ln in (text or "").splitlines() if ln.strip()]):
        tok = re.sub(r"[^0-9A-Za-z]", "", line).upper()
        if tok in ("1", "2", "TIE"):
            return tok
        m = re.search(r"\b(1|2|TIE)\b\W*$", line, re.I)
        if m:
            return m.group(1).upper()
        return None
    return None


def combine(v_off_first, v_on_first):
    """A win needs the same pick in both orders. A flip or a TIE is a tie. Callers handle a missing verdict."""
    a = {"1": "off", "2": "on"}.get(v_off_first or "", "tie")
    b = {"1": "on", "2": "off"}.get(v_on_first or "", "tie")
    return a if a == b and a != "tie" else "tie"


def apply_rule(pairs, tokens_off, tokens_on, rule, total_tasks):
    on_wins = sum(1 for p in pairs if p == "on")
    off_wins = sum(1 for p in pairs if p == "off")
    unjudged = sum(1 for p in pairs if p == "unjudged")
    ratio = (tokens_on / tokens_off) if tokens_off else None
    cap = rule.get("token_ratio_cap")
    complete = len(pairs) == total_tasks and unjudged == 0
    keep = on_wins >= rule["win_bar"] and (cap is None or (ratio is not None and ratio <= cap))
    return {"pairs": len(pairs), "on_wins": on_wins, "off_wins": off_wins,
            "ties": len(pairs) - on_wins - off_wins - unjudged, "unjudged": unjudged,
            "tokens_off": tokens_off, "tokens_on": tokens_on,
            "token_ratio_on_over_off": round(ratio, 3) if ratio else None,
            "win_bar": rule["win_bar"], "token_ratio_cap": cap, "complete": complete,
            "outcome": ("KEEP" if keep else "NOT-KEPT") if complete else "INCOMPLETE"}


# ---------------------------------------------------------------- run / judge / score

def run_arm(cfg, task, arm, out_dir, dry_run=False):
    cmd = arm_command(cfg, arm, task["task"] + PROMPT_TAIL)
    if dry_run:
        print("[dry-run] cd %s && %s" % (task["cwd"], " ".join(shlex.quote(c) for c in cmd)))
        return None
    raw = os.path.join(out_dir, "raw")
    os.makedirs(raw, exist_ok=True)
    base = os.path.join(raw, "%s.%s" % (task["id"], arm))
    t0 = time.time()
    timed_out = False
    try:
        p = subprocess.run(cmd, cwd=task["cwd"], stdin=subprocess.DEVNULL, capture_output=True, text=True,
                           timeout=cfg["arm_timeout"])
        stdout, stderr, rc = p.stdout, p.stderr, p.returncode
    except subprocess.TimeoutExpired as e:
        stdout = e.stdout.decode() if isinstance(e.stdout, bytes) else (e.stdout or "")
        stderr = e.stderr.decode() if isinstance(e.stderr, bytes) else (e.stderr or "")
        rc, timed_out = None, True
    wall = round(time.time() - t0, 1)
    with open(base + ".stream.jsonl", "w") as fh:
        fh.write(stdout)
    facts = parse_stream(stdout, cfg["marker"])
    with open(base + ".answer.md", "w") as fh:
        fh.write(facts["answer"])
    meta = {k: v for k, v in facts.items() if k not in ("answer", "plan_text")}
    meta.update({"task": task["id"], "arm": arm, "cwd": task["cwd"], "exit_code": rc, "wall_s": wall,
                 "stderr_tail": stderr[-800:], "invalid": validity(cfg, arm, facts, timed_out),
                 "mutation_flags": flag_mutations(facts["tool_calls"]),
                 "started_at": dt.datetime.fromtimestamp(t0, dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")})
    with open(base + ".meta.json", "w") as fh:
        json.dump(meta, fh, indent=1)
    print("%s %s: exit=%s wall=%ss tokens=%s hooks=%s marker=%s invalid=%s flags=%d" % (
        task["id"], arm, rc, wall, facts["tokens_total"], facts["hook_events"], facts["marker_seen"],
        meta["invalid"] or "no", len(meta["mutation_flags"])), flush=True)
    return meta


def cmd_run(cfg, tasks, out_dir, dry_run=False):
    for i, t in enumerate(tasks):
        for arm in (ARMS if i % 2 == 0 else ARMS[::-1]):  # alternate which arm runs first (cache and time effects)
            done = None if dry_run else read_meta(out_dir, t["id"], arm)
            if done and not done["invalid"]:
                print("%s %s: already run, kept" % (t["id"], arm), flush=True)
                continue
            meta = run_arm(cfg, t, arm, out_dir, dry_run)
            if meta and meta["invalid"]:
                print("%s %s invalid (%s): one rerun" % (t["id"], arm, "; ".join(meta["invalid"])), flush=True)
                run_arm(cfg, t, arm, out_dir)


def read_meta(out_dir, tid, arm):
    p = os.path.join(out_dir, "raw", "%s.%s.meta.json" % (tid, arm))
    if not os.path.exists(p):
        return None
    with open(p) as fh:
        return json.load(fh)


def read_answer(out_dir, tid, arm):
    with open(os.path.join(out_dir, "raw", "%s.%s.answer.md" % (tid, arm))) as fh:
        return fh.read()


def judge_text(text):
    return (text or "").split("\n[stderr]\n")[0]


def cmd_judge(cfg, tasks, out_dir, dry_run=False):
    jdir = os.path.join(out_dir, "judge")
    cwd = os.path.join(out_dir, "judge-cwd")  # an empty folder: no project instructions reach the judge
    for t in tasks:
        if dry_run:
            for order in ORDERS:
                print("[dry-run] cd %s && %s <prompt for %s, %s>" % (
                    cwd, " ".join(shlex.quote(c) for c in cfg["judge_cmd"]), t["id"], order))
            continue
        moff, mon = read_meta(out_dir, t["id"], "off"), read_meta(out_dir, t["id"], "on")
        if not moff or not mon:
            print("%s: missing a run, not judged" % t["id"])
            continue
        os.makedirs(jdir, exist_ok=True)
        os.makedirs(cwd, exist_ok=True)
        off, on = read_answer(out_dir, t["id"], "off"), read_answer(out_dir, t["id"], "on")
        for order, (one, two) in (("off-first", (off, on)), ("on-first", (on, off))):
            kept = read_verdict(out_dir, t["id"], order)
            if kept is not None:
                print("%s judge %s: already judged (%s), kept" % (t["id"], order, kept), flush=True)
                continue
            prompt = JUDGE_TEMPLATE.format(task=t["task"], known=t["known_right_action"], one=one, two=two)
            t0 = time.time()
            text, rc = "", None
            for attempt in (1, 2):  # one retry when the verdict line is missing
                if attempt == 2:
                    prompt += "\n\nYour previous reply had no verdict line. End with exactly one line: 1, 2, or TIE."
                try:
                    p = subprocess.run(judge_command(cfg, prompt), cwd=cwd, stdin=subprocess.DEVNULL,
                                       capture_output=True, text=True, timeout=cfg["judge_timeout"])
                    out = p.stdout + ("\n[stderr]\n" + p.stderr[-500:] if p.returncode else "")
                    rc = p.returncode
                except subprocess.TimeoutExpired:
                    out, rc = "[timeout]", None
                text += ("\n[retry]\n" if attempt == 2 else "") + out
                if parse_verdict(judge_text(out)) is not None:
                    break
            with open(os.path.join(jdir, "%s.%s.txt" % (t["id"], order)), "w") as fh:
                fh.write(text)
            print("%s judge %s: exit=%s verdict=%s (%.0fs)" % (
                t["id"], order, rc, parse_verdict(judge_text(text.split("\n[retry]\n")[-1])), time.time() - t0),
                flush=True)


def read_verdict(out_dir, tid, order):
    p = os.path.join(out_dir, "judge", "%s.%s.txt" % (tid, order))
    if not os.path.exists(p):
        return None
    with open(p) as fh:
        return parse_verdict(judge_text(fh.read().split("\n[retry]\n")[-1]))


def cmd_score(tasks, out_dir, rule, total_tasks):
    keys = ("tokens_total", "tokens_output", "wall_s", "num_turns", "invalid", "mutation_flags")
    rows, pairs, tok_off, tok_on = [], [], 0, 0
    for t in tasks:
        moff, mon = read_meta(out_dir, t["id"], "off"), read_meta(out_dir, t["id"], "on")
        if not moff or not mon:
            continue
        v_off_first, v_on_first = (read_verdict(out_dir, t["id"], o) for o in ORDERS)
        if moff["invalid"] or mon["invalid"]:
            winner = "tie"  # a run that cannot count never decides a pair
        elif v_off_first is None or v_on_first is None:
            winner = "unjudged"  # the judge did not answer; that is not a verdict of "even"
        else:
            winner = combine(v_off_first, v_on_first)
        pairs.append(winner)
        tok_off += moff["tokens_total"]
        tok_on += mon["tokens_total"]
        rows.append({"task": t["id"], "winner": winner, "verdict_off_first": v_off_first,
                     "verdict_on_first": v_on_first, "off": {k: moff.get(k) for k in keys},
                     "on": {k: mon.get(k) for k in keys}})
    result = apply_rule(pairs, tok_off, tok_on, rule, total_tasks)
    doc = {"schema_version": "placebo_test.result.v1",
           "generated_at": dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
           "rule": result, "tasks": rows}
    with open(os.path.join(out_dir, "results.json"), "w") as fh:
        json.dump(doc, fh, indent=1)
    print(json.dumps(result))
    if result["unjudged"]:
        print("%d pair(s) have no verdict from the judge. Run the judge step again; it only fills in what "
              "is missing." % result["unjudged"])
    for r in rows:
        print("%s winner=%s (off-first=%s, on-first=%s) tokens off=%s on=%s wall off=%ss on=%ss" % (
            r["task"], r["winner"], r["verdict_off_first"], r["verdict_on_first"], r["off"]["tokens_total"],
            r["on"]["tokens_total"], r["off"]["wall_s"], r["on"]["wall_s"]))
    return doc


def build_config(a):
    off_args = shlex.split(a.off_args) if a.off_args is not None else list(DEFAULT_OFF_ARGS)
    on_args = shlex.split(a.on_args or "")
    if off_args == on_args:
        raise SystemExit("--off-args and --on-args are the same: the two runs would not differ")
    return {"claude_bin": a.claude_bin, "model": a.model, "off_args": off_args, "on_args": on_args,
            "marker": a.marker or "", "deny": [d.strip() for d in (a.deny or "").split(",") if d.strip()],
            "arm_timeout": a.arm_timeout, "judge_cmd": shlex.split(a.judge_cmd or ""),
            "judge_timeout": a.judge_timeout}


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("step", choices=["run", "judge", "score", "all"])
    ap.add_argument("--task-file", required=True, help="JSON with the rule and the tasks (see tasks.example.json)")
    ap.add_argument("--out", required=True, help="folder for this trial; a changed rule needs a new one")
    ap.add_argument("--tasks", default="", help="comma-separated ids for a pilot (default: every task)")
    ap.add_argument("--judge-cmd", default="", help="judge command; the prompt is added as its last argument")
    ap.add_argument("--off-args", default=None, help="extra claude arguments for the OFF run "
                                                     "(default: --settings '{\"disableAllHooks\": true}')")
    ap.add_argument("--on-args", default="", help="extra claude arguments for the ON run (default: none)")
    ap.add_argument("--marker", default="", help="text that is in the session only when the thing is loaded")
    ap.add_argument("--deny", default="", help="comma-separated extra tools to disallow in both runs")
    ap.add_argument("--model", default="opus")
    ap.add_argument("--claude-bin", default="claude")
    ap.add_argument("--arm-timeout", type=int, default=480, help="seconds per run (default 480)")
    ap.add_argument("--judge-timeout", type=int, default=300, help="seconds per judge call (default 300)")
    ap.add_argument("--dry-run", action="store_true")
    a = ap.parse_args(argv)
    cfg = build_config(a)
    rule, all_ids, tasks = load_trial(a.task_file, a.tasks or None)
    if a.step in ("judge", "all") and not cfg["judge_cmd"]:
        raise SystemExit("--judge-cmd is required for the judge step")
    lock_rule(a.out, rule, all_ids, write=not a.dry_run)
    if a.step in ("run", "all"):
        cmd_run(cfg, tasks, a.out, a.dry_run)
    if a.step in ("judge", "all"):
        cmd_judge(cfg, tasks, a.out, a.dry_run)
    if a.step in ("score", "all") and not a.dry_run:
        cmd_score(tasks, a.out, rule, len(all_ids))
    return 0


if __name__ == "__main__":
    sys.exit(main())
