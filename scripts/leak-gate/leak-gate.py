#!/usr/bin/env python3
"""leak-gate: refuse a push (or a commit) that would carry a private name.

One implementation, several callers. The git hooks that `install` writes call this
file, and the landing ceremony calls the same file, so they cannot drift.

  leak-gate.py pre-push <remote> <url>     git pre-push hook (ref updates on stdin)
  leak-gate.py range <rev-list args>...    ceremony step, e.g. `range origin/main..HEAD`
  leak-gate.py staged                      pre-commit hook: the staged diff
  leak-gate.py message <file>              commit-msg hook: the commit message
  leak-gate.py install [--repo DIR] [--rules FILE]
                                           write the hooks into DIR's common git dir and point
                                           core.hooksPath at them (every worktree shares them)
  leak-gate.py status [--repo DIR]         show what is installed and which rule file is used

What is scanned (only what is new, never the whole history):
  * every line ADDED by each commit (for a merge: lines that no parent had),
  * the whole content of every added or changed binary file,
  * every added, changed or renamed file path,
  * every commit message, annotated tag message and pushed ref name.
For a push, "new" means: reachable from the pushed commit and not from what the remote
already has (its old tip and its remote-tracking refs). A push to a URL that is not a
configured remote, or to a remote that was never fetched, scans everything reachable.

Rules come from a private TSV file that is never tracked (this file holds no private
token). Lines: `verify<TAB>label<TAB>regex` refuse; `keep<TAB>label<TAB>regex` are
counted and reported only; `drop-path<TAB>prefix` adds a refused path prefix; other kinds
(`regex`, `case`) belong to the publish filter and are ignored here. Regexes are Python
regexes on bytes. Labels that start with `path_` match file paths only.
The rule file is, first match wins:
  1. `git config leakgate.rules <file>`
  2. `<main checkout>/docs_private/plan/tools/publish-public-rules.tsv`, where the main
     checkout is the parent of `git rev-parse --git-common-dir`.
A missing, unreadable or invalid rule file, or one with no `verify` rule, FAILS CLOSED:
the push is refused and the message names the path. There is no "no rules" fallback.

Paths always refused, with or without rules: anything under `docs_private/`,
`.compound-private/` or `.claude/` (at any depth), and any `*.private.md` file.

Output names the category, commit, file and line, never the matched text. Set
LEAK_GATE_SHOW=1 to also print the matched text on the terminal (never into a file).

Override (the only one): LEAK_GATE_OVERRIDE="<reason, 12+ characters>" git push ...
It works only for rule hits, never for a missing rule file. Each use appends one line
(UTC time, repo, mode, refs, categories with counts, actor, reason) to
$COMPOUND_HOME/state/leak-gate/overrides.log (COMPOUND_HOME defaults to ~/.compound).
If that line cannot be written, the push is refused. Refusals are appended (counts only)
to refusals.log in the same folder, best effort.

Remaining hole: `git push --no-verify` skips every hook, so this gate cannot see it.

Exit codes: 0 clean or overridden, 1 refused, 2 failed closed (rules or git error),
64 usage error.
"""

import datetime
import os
import re
import subprocess
import sys
import time

DEFAULT_RULES_REL = os.path.join("docs_private", "plan", "tools", "publish-public-rules.tsv")
PRIVATE_PATH = re.compile(rb"(?:^|/)(?:docs_private|\.compound-private|\.claude)/|\.private\.md$")
ZERO_OID = re.compile(r"^0+$")
MAX_FINDINGS = 200
MIN_REASON = 12
HOOK_MARKER = "leak-gate managed hook"
SHOW = os.environ.get("LEAK_GATE_SHOW") == "1"


class GateError(Exception):
    """Fail closed: rules or git could not be read."""


class Usage(Exception):
    pass


def git(args, cwd=None, inp=None, check=True):
    proc = subprocess.run(["git", *args], cwd=cwd, input=inp, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if check and proc.returncode != 0:
        detail = proc.stderr.decode("utf-8", "replace").strip().splitlines()
        raise GateError("git %s failed: %s" % (" ".join(args[:2]), detail[-1] if detail else "exit %d" % proc.returncode))
    return proc.stdout


def git_text(args, cwd=None, check=True):
    return git(args, cwd=cwd, check=check).decode("utf-8", "surrogateescape").strip()


# ---------------------------------------------------------------- rules

class Rules:
    def __init__(self, path, verify, keep, prefixes):
        self.path = path
        self.verify = verify
        self.keep = keep
        self.prefixes = prefixes


def rules_path(repo=None):
    configured = git_text(["config", "--get", "leakgate.rules"], cwd=repo, check=False)
    if configured:
        return os.path.abspath(os.path.expanduser(configured))
    common = git_text(["rev-parse", "--path-format=absolute", "--git-common-dir"], cwd=repo)
    return os.path.join(os.path.dirname(common.rstrip("/")), DEFAULT_RULES_REL)


def load_rules(path):
    try:
        with open(path, "rb") as handle:
            data = handle.read()
    except OSError as err:
        raise GateError("rule file missing or unreadable: %s (%s)" % (path, err.strerror))
    verify, keep, prefixes = [], [], []
    for number, raw in enumerate(data.split(b"\n"), 1):
        line = raw.rstrip(b"\r")
        if not line.strip() or line.lstrip().startswith(b"#"):
            continue
        parts = line.split(b"\t")
        kind = parts[0].decode("utf-8", "replace")
        if kind in ("verify", "keep"):
            if len(parts) < 3 or not parts[1] or not parts[2]:
                raise GateError("rule file line %d: %s needs a label and a regex (%s)" % (number, kind, path))
            try:
                rx = re.compile(parts[2], re.M)
            except re.error as err:
                raise GateError("rule file line %d: bad regex (%s) (%s)" % (number, err, path))
            (verify if kind == "verify" else keep).append((parts[1].decode("utf-8", "replace"), rx))
        elif kind == "drop-path":
            if len(parts) > 1 and parts[1]:
                prefixes.append(parts[1])
        elif kind in ("regex", "case"):
            continue
        else:
            raise GateError("rule file line %d: unknown kind %r (%s)" % (number, kind, path))
    if not verify:
        raise GateError("rule file has no verify rule, refusing to run without rules: %s" % path)
    return Rules(path, verify, keep, prefixes)


# ---------------------------------------------------------------- scanning

class Scanner:
    def __init__(self, rules):
        cats = [("verify", label, rx) for label, rx in rules.verify] + [("keep", label, rx) for label, rx in rules.keep]
        self.content_cats = [c for c in cats if not c[1].startswith("path_")]
        self.path_cats = cats
        self.prefixes = rules.prefixes
        self.findings = []  # verify hits: (kind, label, commit, where, path, line, matched)
        self.kept = {}  # keep hits are counted only; they never fill the cap or stop the scan
        self.commits = set()
        self.full = False

    def add(self, kind, label, commit, where, path=None, line=None, matched=b""):
        if kind == "keep":
            self.kept[label] = self.kept.get(label, 0) + 1
            return
        if len(self.findings) >= MAX_FINDINGS:
            self.full = True
            return
        self.findings.append((kind, label, commit, where, path, line, matched))

    def scan_text(self, data, commit, where, path=None, line_numbers=None):
        if not data:
            return
        starts = None
        for kind, label, rx in self.content_cats:
            for match in rx.finditer(data):
                line = None
                if line_numbers is not None:
                    if starts is None:
                        starts = [0] + [i + 1 for i, byte in enumerate(data) if byte == 10]
                    index = _bisect(starts, match.start())
                    line = line_numbers[index] if index < len(line_numbers) else None
                self.add(kind, label, commit, where, path, line, match.group(0))

    def scan_path(self, path, commit):
        if PRIVATE_PATH.search(path) or any(path.startswith(p) for p in self.prefixes):
            self.add("verify", "private_path", commit, "path", path)
        for kind, label, rx in self.path_cats:
            match = rx.search(path)
            if match:
                self.add(kind, label, commit, "path", path, None, match.group(0))

    def verify_hits(self):
        return [f for f in self.findings if f[0] == "verify"]


def _bisect(starts, offset):
    lo, hi = 0, len(starts)
    while lo < hi:
        mid = (lo + hi) // 2
        if starts[mid] <= offset:
            lo = mid + 1
        else:
            hi = mid
    return lo - 1


def _unquote(raw):
    """Undo git's C-style path quoting ("a\\tb", octal escapes)."""
    if not (raw.startswith(b'"') and raw.endswith(b'"') and len(raw) >= 2):
        return raw
    out, body, i = bytearray(), raw[1:-1], 0
    simple = {ord("n"): 10, ord("t"): 9, ord("r"): 13, ord("a"): 7, ord("b"): 8, ord("f"): 12, ord("v"): 11, ord("\\"): 92, ord('"'): 34}
    while i < len(body):
        byte = body[i]
        if byte == 92 and i + 1 < len(body):
            nxt = body[i + 1]
            if 48 <= nxt <= 55 and i + 3 < len(body) + 1:
                out.append(int(body[i + 1:i + 4], 8) & 0xFF)
                i += 4
                continue
            out.append(simple.get(nxt, nxt))
            i += 2
            continue
        out.append(byte)
        i += 1
    return bytes(out)


def _strip_side(path):
    path = _unquote(path.rstrip(b"\t"))
    return path[2:] if path[:2] in (b"a/", b"b/") else path


class FileSection:
    def __init__(self, header, combined):
        self.header = header
        self.combined = combined
        self.path = None
        self.deleted = False
        self.binary = False
        self.new_blob = None
        self.parents = 1
        self.in_hunk = False
        self.added = []  # (line number in new file, bytes)
        self.new_line = 0


def _header_path(section):
    rest = section.header
    if section.combined:
        return _unquote(rest)
    if rest.startswith(b'"'):
        end = rest.find(b'" ', 1)
        return _strip_side(rest[end + 2:]) if end > 0 else _strip_side(rest)
    if (len(rest) - 5) % 2 == 0:
        half = (len(rest) - 5) // 2
        if rest[:2] == b"a/" and rest[2 + half:5 + half] == b" b/" and rest[2:2 + half] == rest[5 + half:]:
            return rest[5 + half:]
    return rest


class PatchParser:
    """Stream-parse `git log -p` / `git diff -p` output into the scanner."""

    def __init__(self, scanner, cat_file):
        self.scanner = scanner
        self.cat_file = cat_file
        self.commit = None
        self.msg = None
        self.section = None

    def feed(self, raw):
        line = raw[:-1] if raw.endswith(b"\n") else raw
        if self.msg is not None:
            end = line.find(b"\x02")
            if end < 0:
                self.msg.append(line)
                return
            self.msg.append(line[:end])
            self.scanner.scan_text(b"\n".join(self.msg), self.commit, "message")
            self.msg = None
            return
        if line.startswith(b"\x01"):
            self.finish_file()
            sha, _, first = line[1:].partition(b"\x00")
            self.commit = sha.decode("ascii", "replace")
            self.scanner.commits.add(self.commit)
            self.msg = []
            end = first.find(b"\x02")
            if end >= 0:
                self.msg = None
                self.scanner.scan_text(first[:end], self.commit, "message")
            else:
                self.msg.append(first)
            return
        if line.startswith(b"diff --git "):
            self.finish_file()
            self.section = FileSection(line[len(b"diff --git "):], False)
            return
        if line.startswith(b"diff --cc ") or line.startswith(b"diff --combined "):
            self.finish_file()
            self.section = FileSection(line.split(b" ", 2)[2], True)
            return
        sec = self.section
        if sec is None:
            return
        if line.startswith(b"@@"):
            sec.in_hunk = True
            ats = len(line) - len(line.lstrip(b"@"))
            sec.parents = max(1, ats - 1)
            plus = re.search(rb" \+(\d+)", line)
            sec.new_line = int(plus.group(1)) if plus else 0
            return
        if sec.in_hunk:
            marks = line[:sec.parents]
            if len(marks) < sec.parents or line.startswith(b"\\"):
                return
            if b"-" in marks:
                return
            # Added means "in no parent": every marker column is '+'. A merge line that
            # one parent already had was scanned with that parent's commits (or is on the remote).
            if marks == b"+" * sec.parents:
                sec.added.append((sec.new_line, line[sec.parents:]))
            sec.new_line += 1
            return
        if line.startswith(b"+++ "):
            target = line[4:]
            if target == b"/dev/null":
                sec.deleted = True
            else:
                sec.path = _strip_side(target)
        elif line.startswith(b"rename to ") or line.startswith(b"copy to "):
            sec.path = _unquote(line.split(b" ", 2)[2])
        elif line.startswith(b"deleted file mode"):
            sec.deleted = True
        elif line.startswith(b"index "):
            span = line.split(b" ")[1]
            if b".." in span:
                sec.new_blob = span.split(b"..")[1].decode("ascii", "replace")
        elif line.startswith(b"Binary files ") or line == b"GIT binary patch":
            sec.binary = True

    def finish_file(self):
        sec, self.section = self.section, None
        if sec is None or sec.deleted:
            return
        path = sec.path if sec.path is not None else _header_path(sec)
        self.scanner.scan_path(path, self.commit)
        shown = path.decode("utf-8", "replace")
        if sec.added:
            self.scanner.scan_text(b"\n".join(text for _, text in sec.added), self.commit, "file", shown,
                                   [number for number, _ in sec.added])
        if sec.binary and sec.new_blob and not ZERO_OID.match(sec.new_blob):
            self.scanner.scan_text(self.cat_file(sec.new_blob), self.commit, "binary file", shown)

    def finish(self):
        if self.msg is not None:
            self.scanner.scan_text(b"\n".join(self.msg), self.commit, "message")
            self.msg = None
        self.finish_file()


class CatFile:
    def __init__(self, repo):
        self.repo = repo
        self.proc = None

    def __call__(self, oid):
        if self.proc is None:
            self.proc = subprocess.Popen(["git", "cat-file", "--batch"], cwd=self.repo,
                                         stdin=subprocess.PIPE, stdout=subprocess.PIPE)
        self.proc.stdin.write(oid.encode() + b"\n")
        self.proc.stdin.flush()
        head = self.proc.stdout.readline().split()
        if len(head) < 3 or head[1] == b"missing":
            return b""
        data = self.proc.stdout.read(int(head[2]))
        self.proc.stdout.read(1)
        return data

    def close(self):
        if self.proc is not None:
            self.proc.stdin.close()
            self.proc.wait()


DIFF_ARGS = ["-p", "-M", "--full-index", "--unified=0", "--no-color", "--no-ext-diff", "--no-textconv",
             "--diff-filter=ACMRT"]


def stream(scanner, repo, args, stdin_bytes=None):
    cat = CatFile(repo)
    parser = PatchParser(scanner, cat)
    proc = subprocess.Popen(["git", "-c", "core.quotePath=false", *args], cwd=repo, stdin=subprocess.PIPE,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    try:
        if stdin_bytes is not None:
            proc.stdin.write(stdin_bytes)
        proc.stdin.close()
        for raw in proc.stdout:
            parser.feed(raw)
            if scanner.full:
                proc.kill()
                break
        parser.finish()
    finally:
        err = proc.stderr.read()
        code = proc.wait()
        cat.close()
    if code != 0 and not scanner.full:
        detail = err.decode("utf-8", "replace").strip().splitlines()
        raise GateError("git %s failed: %s" % (args[0], detail[-1] if detail else "exit %d" % code))


def scan_commits(scanner, repo, commits):
    if not commits:
        return
    fmt = "--format=%x01%H%x00%B%x02"
    stream(scanner, repo, ["log", "--no-walk=unsorted", "--stdin", fmt, "--cc", *DIFF_ARGS],
           ("\n".join(commits) + "\n").encode())


def rev_list(repo, args):
    out = git_text(["rev-list", *args], cwd=repo)
    return [line for line in out.splitlines() if line]


# ---------------------------------------------------------------- push range

def configured_remote(repo, remote, url):
    names = git_text(["remote"], cwd=repo, check=False).splitlines()
    if remote in names:
        return remote
    for name in names:
        for key in ("url", "pushurl"):
            for value in git_text(["config", "--get-all", "remote.%s.%s" % (name, key)], cwd=repo, check=False).splitlines():
                if value and value == url:
                    return name
    return None


def object_exists(repo, oid):
    return subprocess.run(["git", "cat-file", "-e", oid + "^{commit}"], cwd=repo,
                          stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0


def push_commits(repo, remote, url, updates):
    """Commits the push would add, plus annotated tag objects and ref names to scan."""
    known = configured_remote(repo, remote, url)
    exclude = ["--remotes=%s" % known] if known else []
    commits, seen, tags, refs = [], set(), [], []
    for local_ref, local_oid, remote_ref, remote_oid in updates:
        if ZERO_OID.match(local_oid):
            continue
        refs.append(remote_ref)
        if git_text(["cat-file", "-t", local_oid], cwd=repo) == "tag":
            tags.append(local_oid)
        args = [local_oid, "--not", *exclude]
        if not ZERO_OID.match(remote_oid) and object_exists(repo, remote_oid):
            args.append(remote_oid)
        for commit in rev_list(repo, args):
            if commit not in seen:
                seen.add(commit)
                commits.append(commit)
    return commits, tags, refs, known


def tag_message(repo, oid):
    body = git(["cat-file", "tag", oid], cwd=repo)
    return body.split(b"\n\n", 1)[1] if b"\n\n" in body else b""


# ---------------------------------------------------------------- report

def actor():
    if os.environ.get("LEAK_GATE_ACTOR"):
        return os.environ["LEAK_GATE_ACTOR"]
    for var, name in (("CLAUDECODE", "claude_code"), ("CODEX_THREAD_ID", "codex"), ("PI_SESSION_ID", "oh_my_pi")):
        if os.environ.get(var):
            return name
    return "user:" + os.environ.get("USER", "unknown")


def state_dir():
    home = os.environ.get("COMPOUND_HOME") or os.path.join(os.path.expanduser("~"), ".compound")
    return os.path.join(home, "state", "leak-gate")


def append_log(name, fields):
    folder = state_dir()
    os.makedirs(folder, mode=0o700, exist_ok=True)
    line = "\t".join([datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")] +
                     ["%s=%s" % (k, str(v).replace("\t", " ").replace("\n", " ")) for k, v in fields])
    with open(os.path.join(folder, name), "a", encoding="utf-8") as handle:
        handle.write(line + "\n")
    return os.path.join(folder, name)


def category_counts(findings):
    counts = {}
    for f in findings:
        counts[f[1]] = counts.get(f[1], 0) + 1
    return ",".join("%s:%d" % (k, counts[k]) for k in sorted(counts))


def describe(f):
    kind, label, commit, where, path, line, matched = f
    if isinstance(path, bytes):
        path = path.decode("utf-8", "replace")
    place = {"message": "commit message", "ref": "ref name", "tag": "tag message"}.get(where)
    if place is None:
        place = "%s %s" % ("path" if where == "path" else where, path or "?")
        if line:
            place += " line %d" % line
    who = "commit %s" % commit[:10] if commit and commit != "staged" else ("staged change" if commit else "")
    text = "  %-22s %s  %s" % (label, who, place)
    if SHOW and matched:
        text += "  [%s]" % matched[:80].decode("utf-8", "replace")
    return text


def verdict(scanner, mode, what, refs_desc, repo, started, quiet_ok=False):
    """Print the result; return the exit code."""
    say = lambda msg: print("leak-gate: " + msg, file=sys.stderr)
    hits = scanner.verify_hits()
    elapsed = time.time() - started
    if scanner.kept:
        say("note: %d kept name(s), not refused (%s)" % (sum(scanner.kept.values()),
                                                         ",".join("%s:%d" % (k, scanner.kept[k]) for k in sorted(scanner.kept))))
    if not hits:
        if not quiet_ok:
            say("ok: %s, %d commit(s) scanned, 0 private hits (%.2f s)" % (what, len(scanner.commits), elapsed))
        return 0
    more = " (stopped after %d)" % MAX_FINDINGS if scanner.full else ""
    say("REFUSED: %s would add private names: %d hit(s)%s in %d categor%s (%.2f s)" % (
        what, len(hits), more, len({h[1] for h in hits}), "y" if len({h[1] for h in hits}) == 1 else "ies", elapsed))
    for f in hits[:25]:
        print(describe(f), file=sys.stderr)
    if len(hits) > 25:
        say("... and %d more" % (len(hits) - 25))
    fields = [("mode", mode), ("repo", repo), ("refs", refs_desc), ("categories", category_counts(hits)), ("actor", actor())]
    reason = (os.environ.get("LEAK_GATE_OVERRIDE") or "").strip()
    if reason:
        if len(reason) < MIN_REASON:
            say("override refused: LEAK_GATE_OVERRIDE needs a reason of %d+ characters" % MIN_REASON)
        else:
            try:
                where = append_log("overrides.log", fields + [("reason", reason)])
            except OSError as err:
                say("override refused: cannot record it (%s)" % err.strerror)
            else:
                say("OVERRIDDEN with reason %r; recorded in %s" % (reason, where))
                return 0
    try:
        append_log("refusals.log", fields)
    except OSError:
        pass
    if mode == "pre-push":
        say("nothing was pushed. Remove the names from the commits above (they are not on the remote yet),")
        say("or, only if this is truly intended: LEAK_GATE_OVERRIDE=\"<reason>\" git push ...  (recorded)")
    elif mode in ("staged", "message"):
        say("commit stopped. Remove the names, or LEAK_GATE_OVERRIDE=\"<reason>\" git commit ...  (recorded)")
    else:
        say("do not push this range. Remove the names from the commits above first.")
    return 1


# ---------------------------------------------------------------- commands

def toplevel(repo=None):
    top = git_text(["rev-parse", "--show-toplevel"], cwd=repo, check=False)
    return top or git_text(["rev-parse", "--path-format=absolute", "--git-common-dir"], cwd=repo)


def cmd_pre_push(argv):
    if len(argv) < 2:
        raise Usage("pre-push needs <remote> <url>")
    remote, url = argv[0], argv[1]
    started = time.time()
    updates = []
    for raw in sys.stdin.read().splitlines():
        parts = raw.split()
        if len(parts) == 4:
            updates.append(parts)
        elif raw.strip():
            raise GateError("unexpected pre-push input line: %r" % raw[:120])
    rules = load_rules(rules_path())
    scanner = Scanner(rules)
    commits, tags, refs, known = push_commits(None, remote, url, updates)
    for ref in refs:
        scanner.scan_text(ref.encode("utf-8", "surrogateescape"), None, "ref")
    for tag in tags:
        scanner.scan_text(tag_message(None, tag), None, "tag")
    scan_commits(scanner, None, commits)
    target = known or "an unconfigured remote"
    # Log tips, not ref names: a ref name can itself be the private hit.
    refs_desc = "%d ref(s) tips=%s" % (len(refs), ",".join(u[1][:12] for u in updates if not ZERO_OID.match(u[1])))
    return verdict(scanner, "pre-push", "push to %s (%s)" % (target, ", ".join(refs) or "no refs"), refs_desc, toplevel(), started)


def cmd_range(argv):
    if not argv:
        raise Usage("range needs rev-list arguments, e.g. origin/main..HEAD")
    started = time.time()
    rules = load_rules(rules_path())
    scanner = Scanner(rules)
    scan_commits(scanner, None, rev_list(None, argv))
    return verdict(scanner, "range", "range %s" % " ".join(argv), "%d commit(s)" % len(scanner.commits), toplevel(), started)


def cmd_staged(argv):
    started = time.time()
    rules = load_rules(rules_path())
    scanner = Scanner(rules)
    scanner.commits.add("staged")
    parser_commit = "staged"
    cat = CatFile(None)
    parser = PatchParser(scanner, cat)
    parser.commit = parser_commit
    out = git(["-c", "core.quotePath=false", "diff", "--cached", *DIFF_ARGS])
    for raw in out.splitlines(keepends=True):
        parser.feed(raw)
    parser.finish()
    cat.close()
    return verdict(scanner, "staged", "staged change", "index", toplevel(), started, quiet_ok=True)


def cmd_message(argv):
    if len(argv) != 1:
        raise Usage("message needs <file>")
    started = time.time()
    rules = load_rules(rules_path())
    scanner = Scanner(rules)
    with open(argv[0], "rb") as handle:
        lines = []
        for line in handle.read().split(b"\n"):
            if line.startswith(b"# ------------------------ >8"):
                break
            if not line.startswith(b"#"):
                lines.append(line)
    scanner.scan_text(b"\n".join(lines), None, "message")
    return verdict(scanner, "message", "commit message", "message", toplevel(), started, quiet_ok=True)


HOOK_TEMPLATE = r"""#!/bin/sh
# __MARKER__ (__NAME__). Written by scripts/leak-gate/leak-gate.py install; re-run it to update.
# Runs the leak gate, then the global core.hooksPath hook of the same name (if any).
gate=
common=$(git rev-parse --path-format=absolute --git-common-dir 2>/dev/null)
top=$(git rev-parse --show-toplevel 2>/dev/null)
for c in "${common%/}/../scripts/leak-gate/leak-gate.py" "${top:-/nonexistent}/scripts/leak-gate/leak-gate.py" "$(git config --get leakgate.script 2>/dev/null)"; do
  if [ -n "$c" ] && [ -f "$c" ]; then gate=$c; break; fi
done
py=/usr/bin/python3
[ -x "$py" ] || py=$(command -v python3 2>/dev/null)
chain() {
  [ -n "$LEAK_GATE_CHAIN" ] && return 0
  g=$(git config --global --get core.hooksPath 2>/dev/null) || return 0
  case $g in "~/"*) g="$HOME/${g#\~/}";; esac
  g=${g%/}
  [ -x "$g/__NAME__" ] || return 0
  here=$(cd "$(dirname "$0")" 2>/dev/null && pwd -P)
  [ "$(cd "$g" 2>/dev/null && pwd -P)" = "$here" ] && return 0
  n=${GIT_CONFIG_COUNT:-0}
  env LEAK_GATE_CHAIN=1 "GIT_CONFIG_KEY_$n=core.hooksPath" "GIT_CONFIG_VALUE_$n=$g" "GIT_CONFIG_COUNT=$((n + 1))" "$g/__NAME__" "$@"
}
__BODY__
"""

HOOK_BODIES = {
    "pre-push": r"""input=$(cat)
if [ -z "$gate" ] || [ -z "$py" ]; then
  echo "leak-gate: REFUSED: gate script or python3 not found, so the push is refused (fail closed)." >&2
  exit 1
fi
printf '%s\n' "$input" | "$py" -I "$gate" pre-push "$@" || exit 1
printf '%s\n' "$input" | chain "$@"
""",
    "pre-commit": r"""if [ -n "$gate" ] && [ -n "$py" ]; then
  "$py" -I "$gate" staged; rc=$?
  [ "$rc" -eq 1 ] && exit 1
  [ "$rc" -ne 0 ] && echo "leak-gate: warning: commit check could not run (exit $rc); the push gate still runs." >&2
else
  echo "leak-gate: warning: gate script not found; the push gate will refuse until it is back." >&2
fi
chain "$@"
""",
    "commit-msg": r"""if [ -n "$gate" ] && [ -n "$py" ]; then
  "$py" -I "$gate" message "$1"; rc=$?
  [ "$rc" -eq 1 ] && exit 1
  [ "$rc" -ne 0 ] && echo "leak-gate: warning: message check could not run (exit $rc); the push gate still runs." >&2
fi
chain "$@"
""",
}


def hooks_dir(repo):
    common = git_text(["rev-parse", "--path-format=absolute", "--git-common-dir"], cwd=repo)
    return os.path.join(common.rstrip("/"), "hooks")


def cmd_install(argv):
    repo, rules_file = None, None
    args = list(argv)
    while args:
        flag = args.pop(0)
        if flag == "--repo" and args:
            repo = args.pop(0)
        elif flag == "--rules" and args:
            rules_file = os.path.abspath(os.path.expanduser(args.pop(0)))
        else:
            raise Usage("install [--repo DIR] [--rules FILE]")
    folder = hooks_dir(repo)
    os.makedirs(folder, exist_ok=True)
    for name, body in HOOK_BODIES.items():
        target = os.path.join(folder, name)
        if os.path.exists(target):
            with open(target, encoding="utf-8", errors="replace") as handle:
                if HOOK_MARKER not in handle.read():
                    raise GateError("%s exists and is not a leak-gate hook; move it aside first" % target)
        text = HOOK_TEMPLATE.replace("__BODY__", body).replace("__NAME__", name).replace("__MARKER__", HOOK_MARKER)
        tmp = target + ".tmp"
        with open(tmp, "w", encoding="utf-8") as handle:
            handle.write(text)
        os.chmod(tmp, 0o755)
        os.replace(tmp, target)
    git(["config", "--local", "core.hooksPath", folder], cwd=repo)
    git(["config", "--local", "leakgate.script", os.path.abspath(__file__)], cwd=repo)
    if rules_file:
        git(["config", "--local", "leakgate.rules", rules_file], cwd=repo)
    print("leak-gate: installed %s in %s; core.hooksPath set in the shared repo config" % (", ".join(HOOK_BODIES), folder))
    return cmd_status(["--repo", repo] if repo else [])


def cmd_status(argv):
    repo = argv[1] if len(argv) == 2 and argv[0] == "--repo" else None
    if argv and repo is None:
        raise Usage("status [--repo DIR]")
    folder = hooks_dir(repo)
    effective = git_text(["config", "--get", "core.hooksPath"], cwd=repo, check=False)
    ok = os.path.realpath(os.path.expanduser(effective or "/nonexistent")) == os.path.realpath(folder)
    print("hooks dir:      %s" % folder)
    print("core.hooksPath: %s (%s)" % (effective or "(unset)", "points at it" if ok else "NOT pointing at it"))
    for name in HOOK_BODIES:
        path = os.path.join(folder, name)
        managed = os.path.exists(path) and HOOK_MARKER in open(path, encoding="utf-8", errors="replace").read()
        ok = ok and managed and os.access(path, os.X_OK)
        print("hook %-11s %s" % (name + ":", "installed" if managed else "MISSING"))
    path = rules_path(repo)
    try:
        rules = load_rules(path)
        print("rules:          %s (%d verify, %d keep, %d extra path prefixes)" % (path, len(rules.verify), len(rules.keep), len(rules.prefixes)))
    except GateError as err:
        ok = False
        print("rules:          FAIL CLOSED: %s" % err)
    return 0 if ok else 2


COMMANDS = {"pre-push": cmd_pre_push, "range": cmd_range, "staged": cmd_staged, "message": cmd_message,
            "install": cmd_install, "status": cmd_status}


def main(argv):
    if not argv or argv[0] not in COMMANDS:
        print(__doc__, file=sys.stderr)
        return 64
    try:
        return COMMANDS[argv[0]](argv[1:])
    except Usage as err:
        print("leak-gate: usage: %s" % err, file=sys.stderr)
        return 64
    except GateError as err:
        print("leak-gate: REFUSED (fail closed): %s" % err, file=sys.stderr)
        print("leak-gate: the rule file must exist at the path above, or set `git config leakgate.rules <file>`.", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
