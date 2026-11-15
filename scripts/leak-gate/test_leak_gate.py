#!/usr/bin/env python3
"""Tests for leak-gate.py, run through real git hooks in throwaway repositories.

Synthetic rules and made-up tokens only; these tests never read the real rule file.
Run: python3 -I scripts/leak-gate/test_leak_gate.py
"""

import os
import shutil
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
GATE = os.path.join(HERE, "leak-gate.py")

RULES = "\n".join([
    "# synthetic rules for the leak-gate tests",
    "regex\tignoredpattern\tignoredreplacement",
    "case\tignoredcase\tignored words",
    "drop-path\tvault_private/",
    "verify\tfake_product\t(?i)zorb[-_ ]?lax",
    "verify\tfake_host\t(?<![a-z])quarkbox(?![a-z])",
    "verify\tpath_secretdir\t^secretdir/",
    "keep\tfake_tool\t(?i)quuxtool",
]) + "\n"
TOKEN = "Zorblax"


class GateRepo(unittest.TestCase):
    """A work repository with the hooks installed and a bare 'origin' that has main."""

    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="leak-gate-test-")
        self.home = os.path.join(self.tmp, "home")
        os.makedirs(self.home)
        with open(os.path.join(self.home, ".gitconfig"), "w") as handle:
            handle.write("[user]\n\tname = Synthetic\n\temail = proof@example.invalid\n"
                         "[init]\n\tdefaultBranch = main\n[commit]\n\tgpgsign = false\n[tag]\n\tgpgsign = false\n")
        self.env = {k: v for k, v in os.environ.items() if not k.startswith(("GIT_", "LEAK_GATE_"))}
        self.env.update(HOME=self.home, GIT_CONFIG_NOSYSTEM="1", COMPOUND_HOME=os.path.join(self.tmp, "compound-home"))
        self.env.pop("COMPOUND_WORK_ID", None)
        self.rules = os.path.join(self.tmp, "rules.tsv")
        with open(self.rules, "w") as handle:
            handle.write(RULES)
        self.remote = os.path.join(self.tmp, "remote.git")
        self.work = os.path.join(self.tmp, "work")
        self.git(["init", "-q", "--bare", self.remote], cwd=self.tmp)
        self.git(["init", "-q", self.work], cwd=self.tmp)
        self.write("README.txt", "base\n")
        self.git(["add", "README.txt"])
        self.git(["commit", "-q", "-m", "base"])
        self.git(["remote", "add", "origin", self.remote])
        self.git(["push", "-q", "origin", "main"])
        self.git(["fetch", "-q", "origin"])
        out = self.run_gate(["install", "--repo", self.work, "--rules", self.rules])
        self.assertEqual(out.returncode, 0, out.stdout + out.stderr)

    def tearDown(self):
        shutil.rmtree(self.tmp, ignore_errors=True)

    # helpers ---------------------------------------------------------------

    def git(self, args, cwd=None, env=None, check=True):
        proc = subprocess.run(["git", *args], cwd=cwd or self.work, env=env or self.env,
                              stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        if check and proc.returncode != 0:
            raise AssertionError("git %s failed: %s" % (" ".join(args), proc.stderr))
        return proc

    def run_gate(self, args, cwd=None, env=None, stdin=None):
        return subprocess.run([sys.executable, "-I", GATE, *args], cwd=cwd or self.work, env=env or self.env,
                              input=stdin, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)

    def write(self, rel, text, mode="w"):
        path = os.path.join(self.work, rel)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, mode) as handle:
            handle.write(text)

    def commit(self, files, message="change", verify=True):
        for rel, text in files.items():
            self.write(rel, text, "wb" if isinstance(text, bytes) else "w")
            self.git(["add", "--", rel])
        args = ["commit", "-q", "-m", message] + ([] if verify else ["--no-verify"])
        self.git(args)
        return self.git(["rev-parse", "HEAD"]).stdout.strip()

    def branch(self, name):
        self.git(["checkout", "-q", "-b", name, "origin/main"])

    def push(self, *refspecs, env_extra=None, remote="origin"):
        env = dict(self.env, **(env_extra or {}))
        return self.git(["push", remote, *refspecs], env=env, check=False)

    def remote_has(self, ref):
        return self.git(["rev-parse", "-q", "--verify", ref], cwd=self.remote, check=False).returncode == 0

    def assertRefused(self, proc, *needles):
        self.assertNotEqual(proc.returncode, 0, "push was accepted:\n" + proc.stderr)
        for needle in needles:
            self.assertIn(needle, proc.stderr)

    def assertAccepted(self, proc):
        self.assertEqual(proc.returncode, 0, "push was refused:\n" + proc.stderr)


class PushGateTest(GateRepo):

    def test_clean_push_is_accepted_and_timed(self):
        self.branch("clean")
        self.commit({"notes/a.txt": "nothing private here\n"})
        proc = self.push("clean")
        self.assertAccepted(proc)
        self.assertIn("leak-gate: ok", proc.stderr)
        self.assertTrue(self.remote_has("refs/heads/clean"))

    def test_added_line_is_refused_with_category_commit_file_and_line(self):
        self.branch("leak")
        sha = self.commit({"notes/a.txt": "one\ntwo\nhello %s world\n" % TOKEN}, verify=False)
        proc = self.push("leak")
        self.assertRefused(proc, "fake_product", sha[:10], "file notes/a.txt line 3", "nothing was pushed")
        self.assertNotIn(TOKEN, proc.stderr, "matched text must not be printed by default")
        self.assertFalse(self.remote_has("refs/heads/leak"))

    def test_show_prints_matched_text_on_terminal_only(self):
        self.branch("leak")
        self.commit({"a.txt": "%s\n" % TOKEN}, verify=False)
        proc = self.push("leak", env_extra={"LEAK_GATE_SHOW": "1"})
        self.assertRefused(proc, TOKEN)

    def test_commit_message_is_refused(self):
        self.branch("msg")
        self.commit({"a.txt": "clean\n"}, message="update the %s page" % TOKEN.lower(), verify=False)
        self.assertRefused(self.push("msg"), "fake_product", "commit message")

    def test_word_boundary_pattern_does_not_overmatch(self):
        self.branch("near")
        self.commit({"a.txt": "a quarkboxes list\n"})
        self.assertAccepted(self.push("near"))

    def test_many_kept_names_do_not_hide_a_real_hit(self):
        self.branch("flood")
        self.commit({"a.txt": "quuxtool\n" * 400 + "%s\n" % TOKEN}, verify=False)
        self.assertRefused(self.push("flood"), "fake_product", "file a.txt line 401")

    def test_path_category_is_refused(self):
        self.branch("path")
        self.commit({"secretdir/x.txt": "clean\n"}, verify=False)
        self.assertRefused(self.push("path"), "path_secretdir", "path secretdir/x.txt")

    def test_private_paths_are_always_refused(self):
        for index, rel in enumerate((".claude/settings.json", "docs_private/n.md", "a/b/docs_private/n.md",
                    ".compound-private/x.txt", "notes/plan.private.md", "vault_private/x.txt")):
            with self.subTest(rel=rel):
                name = "private-path-%d" % index
                self.branch(name)
                self.commit({rel: "clean\n"}, verify=False)
                self.assertRefused(self.push(name), "private_path")
                self.git(["checkout", "-q", "main"])

    def test_keep_category_is_reported_not_refused(self):
        self.branch("keep")
        self.commit({"a.txt": "uses quuxtool here\n"})
        proc = self.push("keep")
        self.assertAccepted(proc)
        self.assertIn("kept name", proc.stderr)

    def test_only_new_commits_are_scanned(self):
        # A name that is already on the remote (pushed before the gate existed) does not
        # block a later clean push of the same branch.
        self.git(["checkout", "-q", "main"])
        self.commit({"old.txt": "%s\n" % TOKEN}, verify=False)
        self.assertAccepted(self.push("main", env_extra={"LEAK_GATE_OVERRIDE": "seed a pre-gate leak for the test"}))
        self.commit({"new.txt": "clean\n"})
        self.assertAccepted(self.push("main"))

    def test_unconfigured_url_scans_everything_reachable(self):
        self.git(["checkout", "-q", "main"])
        self.commit({"old.txt": "%s\n" % TOKEN}, verify=False)
        self.assertAccepted(self.push("main", env_extra={"LEAK_GATE_OVERRIDE": "seed a pre-gate leak for the test"}))
        fresh = os.path.join(self.tmp, "fresh.git")
        self.git(["init", "-q", "--bare", fresh], cwd=self.tmp)
        self.assertRefused(self.push("main", remote=fresh), "fake_product", "an unconfigured remote")

    def test_evil_merge_content_is_refused(self):
        self.branch("side")
        self.commit({"side.txt": "side\n"})
        self.assertAccepted(self.push("side"))
        self.branch("feature")
        self.commit({"feature.txt": "feature\n"})
        self.git(["merge", "-q", "--no-ff", "--no-commit", "origin/side"])
        self.write("feature.txt", "feature\n%s added only in the merge\n" % TOKEN)
        self.git(["add", "feature.txt"])
        self.git(["commit", "-q", "--no-verify", "-m", "merge side"])
        merge = self.git(["rev-parse", "HEAD"]).stdout.strip()
        self.assertRefused(self.push("feature"), "fake_product", merge[:10], "file feature.txt line 2")

    def test_clean_merge_of_content_already_on_remote_is_accepted(self):
        self.branch("side")
        self.commit({"side.txt": "%s\n" % TOKEN}, verify=False)
        self.assertAccepted(self.push("side", env_extra={"LEAK_GATE_OVERRIDE": "seed a pre-gate leak for the test"}))
        self.branch("feature")
        self.commit({"feature.txt": "feature\n"})
        self.git(["merge", "-q", "--no-ff", "-m", "merge side", "origin/side"])
        self.assertAccepted(self.push("feature"))

    def test_binary_file_content_is_refused(self):
        self.branch("bin")
        self.commit({"img.bin": b"\x00\x01\x02" + TOKEN.encode() + b"\x00\xff"}, verify=False)
        self.assertRefused(self.push("bin"), "fake_product", "binary file img.bin")

    def test_rename_without_new_content_is_accepted(self):
        self.git(["checkout", "-q", "main"])
        self.commit({"old/name.txt": "line one\n%s\nline three\n" % TOKEN}, verify=False)
        self.assertAccepted(self.push("main", env_extra={"LEAK_GATE_OVERRIDE": "seed a pre-gate leak for the test"}))
        os.makedirs(os.path.join(self.work, "new"))
        self.git(["mv", "old/name.txt", "new/name.txt"])
        self.git(["commit", "-q", "-m", "move"])
        self.assertAccepted(self.push("main"))

    def test_deleting_a_private_path_is_accepted(self):
        self.git(["checkout", "-q", "main"])
        self.commit({"docs_private/old.md": "x\n"}, verify=False)
        self.assertAccepted(self.push("main", env_extra={"LEAK_GATE_OVERRIDE": "seed a pre-gate leak for the test"}))
        self.git(["rm", "-q", "docs_private/old.md"])
        self.git(["commit", "-q", "-m", "remove the private file"])
        self.assertAccepted(self.push("main"))

    def test_ref_name_is_refused(self):
        self.branch("ok")
        self.commit({"a.txt": "clean\n"})
        self.assertRefused(self.push("HEAD:refs/heads/feature/zorblax-promo"), "fake_product", "ref name")

    def test_annotated_tag_message_is_refused(self):
        self.git(["checkout", "-q", "main"])
        self.git(["tag", "-a", "v9.9.9", "-m", "release notes for %s" % TOKEN])
        self.assertRefused(self.push("refs/tags/v9.9.9"), "fake_product", "tag message")

    def test_author_committer_and_tagger_identity_are_not_scanned(self):
        # Identity headers may carry the operator's own mail on purpose; only message bodies count.
        # The address is configured, so the attribution check accepts it and only the rules are tested.
        ident = "quarkbox@example.invalid"
        self.git(["config", "--add", "leakgate.identity", ident])
        env = dict(self.env, GIT_AUTHOR_EMAIL=ident, GIT_COMMITTER_EMAIL=ident)
        self.branch("ident")
        self.write("a.txt", "clean\n")
        self.git(["add", "a.txt"], env=env)
        self.git(["commit", "-q", "-m", "clean message"], env=env)
        self.git(["tag", "-a", "v8.8.8", "-m", "clean tag message"], env=env)
        self.assertAccepted(self.push("ident", "refs/tags/v8.8.8"))

    def test_ref_deletion_is_not_scanned(self):
        self.branch("gone")
        self.commit({"a.txt": "clean\n"})
        self.assertAccepted(self.push("gone"))
        self.assertAccepted(self.push(":gone"))


class FailClosedTest(GateRepo):

    def test_missing_rules_refuses_and_names_the_path_even_with_override(self):
        self.branch("clean")
        self.commit({"a.txt": "clean\n"})
        os.rename(self.rules, self.rules + ".hidden")
        proc = self.push("clean", env_extra={"LEAK_GATE_OVERRIDE": "an override must not bypass missing rules"})
        self.assertRefused(proc, "fail closed", self.rules)
        self.assertFalse(self.remote_has("refs/heads/clean"))
        os.rename(self.rules + ".hidden", self.rules)
        self.assertAccepted(self.push("clean"))

    def test_rules_without_verify_fail_closed(self):
        with open(self.rules, "w") as handle:
            handle.write("keep\tfake_tool\tquuxtool\n")
        self.branch("clean")
        self.commit({"a.txt": "clean\n"})
        self.assertRefused(self.push("clean"), "no verify rule")

    def test_bad_regex_fails_closed(self):
        with open(self.rules, "a") as handle:
            handle.write("verify\tbroken\t(unclosed\n")
        self.branch("clean")
        self.commit({"a.txt": "clean\n"})
        self.assertRefused(self.push("clean"), "bad regex")

    def test_missing_gate_script_refuses_push(self):
        self.git(["config", "leakgate.script", os.path.join(self.tmp, "no-such-gate.py")])
        self.branch("clean")
        self.commit({"a.txt": "clean\n"}, verify=False)
        self.assertRefused(self.push("clean"), "fail closed")


class OverrideTest(GateRepo):

    def log_lines(self):
        path = os.path.join(self.env["COMPOUND_HOME"], "state", "leak-gate", "overrides.log")
        if not os.path.exists(path):
            return []
        with open(path) as handle:
            return handle.read().splitlines()

    def test_override_with_reason_is_recorded_without_the_matched_text(self):
        self.branch("leak")
        self.commit({"a.txt": "%s\n" % TOKEN}, verify=False)
        reason = "synthetic test of the recorded override path"
        proc = self.push("leak", env_extra={"LEAK_GATE_OVERRIDE": reason, "LEAK_GATE_ACTOR": "test:actor"})
        self.assertAccepted(proc)
        self.assertIn("OVERRIDDEN", proc.stderr)
        lines = self.log_lines()
        self.assertEqual(len(lines), 1)
        self.assertIn("reason=" + reason, lines[0])
        self.assertIn("categories=fake_product:1", lines[0])
        self.assertIn("actor=test:actor", lines[0])
        self.assertIn("mode=pre-push", lines[0])
        self.assertNotIn(TOKEN.lower(), lines[0].lower())

    def test_override_without_a_real_reason_is_refused(self):
        self.branch("leak")
        self.commit({"a.txt": "%s\n" % TOKEN}, verify=False)
        self.assertRefused(self.push("leak", env_extra={"LEAK_GATE_OVERRIDE": "x"}), "needs a reason")
        self.assertEqual(self.log_lines(), [])

    def test_refusals_are_logged_with_counts_only(self):
        self.branch("leak")
        self.commit({"a.txt": "%s\n" % TOKEN}, verify=False)
        self.push("leak")
        with open(os.path.join(self.env["COMPOUND_HOME"], "state", "leak-gate", "refusals.log")) as handle:
            body = handle.read()
        self.assertIn("categories=fake_product:1", body)
        self.assertNotIn(TOKEN.lower(), body.lower())


class CommitHooksTest(GateRepo):

    def test_pre_commit_refuses_a_staged_name(self):
        self.write("a.txt", "%s\n" % TOKEN)
        self.git(["add", "a.txt"])
        proc = self.git(["commit", "-q", "-m", "x"], check=False)
        self.assertNotEqual(proc.returncode, 0)
        self.assertIn("fake_product", proc.stderr)
        self.assertIn("staged change", proc.stderr)
        self.assertEqual(self.git(["rev-list", "--count", "HEAD"]).stdout.strip(), "1")

    def test_commit_msg_refuses_a_name_in_the_message(self):
        self.write("a.txt", "clean\n")
        self.git(["add", "a.txt"])
        proc = self.git(["commit", "-q", "-m", "about %s" % TOKEN], check=False)
        self.assertNotEqual(proc.returncode, 0)
        self.assertIn("commit message", proc.stderr)

    def test_clean_commit_is_quiet(self):
        self.write("a.txt", "clean\n")
        self.git(["add", "a.txt"])
        proc = self.git(["commit", "-q", "-m", "clean change"])
        self.assertNotIn("leak-gate", proc.stderr)

    def test_global_hooks_still_run_once_and_do_not_loop(self):
        # The global hooks mimic a chain-only design: they look up the repo hook with
        # `git rev-parse --git-path hooks/<name>` and run it, then do their own work.
        ghooks = os.path.join(self.home, "global-hooks")
        os.makedirs(ghooks)
        counter = os.path.join(self.tmp, "global-runs.txt")
        for name in ("pre-push", "commit-msg"):
            body = ("#!/bin/sh\n"
                    "repo_hook=\"$(git rev-parse --git-path hooks/%s)\"\n"
                    "if [ -x \"$repo_hook\" ] && [ \"$repo_hook\" != \"$0\" ]; then \"$repo_hook\" \"$@\" || exit 1; fi\n"
                    "echo %s >> %s\n") % (name, name, counter)
            if name == "commit-msg":
                body += "git interpret-trailers --in-place --trailer 'Chained: yes' \"$1\"\n"
            else:
                body += "cat >/dev/null\n"
            path = os.path.join(ghooks, name)
            with open(path, "w") as handle:
                handle.write(body)
            os.chmod(path, 0o755)
        self.git(["config", "--global", "core.hooksPath", ghooks])
        self.branch("chain")
        self.commit({"a.txt": "clean\n"}, message="chained commit")
        self.assertIn("Chained: yes", self.git(["log", "-1", "--format=%B"]).stdout)
        self.assertAccepted(self.push("chain"))
        with open(counter) as handle:
            self.assertEqual(sorted(handle.read().split()), ["commit-msg", "pre-push"])
        self.commit({"b.txt": "%s\n" % TOKEN}, verify=False)
        self.assertRefused(self.push("chain"), "fake_product")


class AttributionTest(GateRepo):
    """A commit is by the configured identity only and never carries a co-author line."""

    HELPER = "helper@example.invalid"

    def try_commit(self, message, args=(), env_extra=None, git_args=()):
        self.write("a.txt", "change %d\n" % self.counter())
        self.git(["add", "a.txt"])
        env = dict(self.env, **(env_extra or {}))
        return self.git([*git_args, "commit", "-q", "-m", message, *args], env=env, check=False)

    def counter(self):
        self._n = getattr(self, "_n", 0) + 1
        return self._n

    def head_count(self):
        return int(self.git(["rev-list", "--count", "HEAD"]).stdout.strip())

    def assertCommitRefused(self, proc, label, before):
        self.assertNotEqual(proc.returncode, 0, "commit was accepted:\n" + proc.stderr)
        self.assertIn(label, proc.stderr)
        self.assertIn("Compound-Harness:", proc.stderr, "the refusal must say what to do instead")
        self.assertEqual(self.head_count(), before)

    def test_co_authored_by_trailer_is_refused_at_commit(self):
        before = self.head_count()
        proc = self.try_commit("fix: a change\n\nCo-Authored-By: Helper <%s>" % self.HELPER)
        self.assertCommitRefused(proc, "co_author_trailer", before)

    def test_co_authored_by_in_any_capitalisation_is_refused(self):
        for line in ("co-authored-by: Helper <%s>", "CO-AUTHORED-BY: Helper <%s>", "Co-authored-by : Helper <%s>",
                     "  co-Authored-BY:Helper <%s>"):
            with self.subTest(line=line):
                before = self.head_count()
                proc = self.try_commit("fix: a change\n\nCompound-Harness: claude_code\n" + line % self.HELPER)
                self.assertCommitRefused(proc, "co_author_trailer", before)

    def test_phrase_inside_prose_is_accepted(self):
        message = ("docs: say why co-authored-by: lines are refused\n\n"
                   "The gate refuses a Co-authored-by: trailer, so this sentence only names it.")
        proc = self.try_commit(message)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertNotIn("leak-gate", proc.stderr)

    def test_foreign_author_is_refused_at_commit(self):
        before = self.head_count()
        proc = self.try_commit("fix: a change", args=["--author", "Helper <%s>" % self.HELPER])
        self.assertCommitRefused(proc, "foreign_author", before)

    def test_foreign_author_from_environment_is_refused_at_commit(self):
        before = self.head_count()
        proc = self.try_commit("fix: a change", env_extra={"GIT_AUTHOR_EMAIL": self.HELPER})
        self.assertCommitRefused(proc, "foreign_author", before)

    def test_foreign_committer_is_refused_at_commit(self):
        before = self.head_count()
        proc = self.try_commit("fix: a change", env_extra={"GIT_COMMITTER_EMAIL": self.HELPER})
        self.assertCommitRefused(proc, "foreign_committer", before)

    def test_command_line_identity_is_not_a_configured_identity(self):
        before = self.head_count()
        proc = self.try_commit("fix: a change", git_args=["-c", "user.email=%s" % self.HELPER])
        self.assertCommitRefused(proc, "foreign_author", before)
        self.assertIn("foreign_committer", proc.stderr)

    def test_configured_identity_passes_quietly(self):
        proc = self.try_commit("fix: a change\n\nCompound-Harness: claude_code")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertNotIn("leak-gate", proc.stderr)

    def test_extra_identity_from_git_config_passes(self):
        self.git(["config", "--add", "leakgate.identity", "second@example.invalid"])
        proc = self.try_commit("fix: a change", args=["--author", "Synthetic <SECOND@example.invalid>"])
        self.assertEqual(proc.returncode, 0, proc.stderr)

    def test_owner_merge_and_revert_commits_pass(self):
        self.branch("side")
        self.commit({"side.txt": "side\n"}, message="side change")
        self.branch("feature")
        self.commit({"feature.txt": "feature\n"}, message="feature change")
        merge = self.git(["merge", "--no-ff", "-m", "Merge branch 'side' into feature", "side"], check=False)
        self.assertEqual(merge.returncode, 0, merge.stderr)
        self.assertEqual(len(self.git(["log", "-1", "--format=%P"]).stdout.split()), 2)
        revert = self.git(["revert", "--no-edit", "HEAD~1"], check=False)
        self.assertEqual(revert.returncode, 0, revert.stderr)
        self.assertAccepted(self.push("feature"))
        self.assertTrue(self.remote_has("refs/heads/feature"))

    def test_push_refuses_a_range_with_an_offending_commit_and_names_it(self):
        self.branch("attrib")
        self.commit({"a.txt": "clean\n"}, message="clean change")
        trailer = self.commit({"b.txt": "clean\n"}, verify=False,
                              message="fix: b\n\nCo-authored-by: Helper <%s>" % self.HELPER)
        self.write("c.txt", "clean\n")
        self.git(["add", "c.txt"])
        self.git(["commit", "-q", "--no-verify", "-m", "fix: c", "--author", "Helper <%s>" % self.HELPER])
        foreign = self.git(["rev-parse", "HEAD"]).stdout.strip()
        proc = self.push("attrib", env_extra={"LEAK_GATE_OVERRIDE": "an override must not cover attribution"})
        self.assertRefused(proc, "co_author_trailer", "commit %s" % trailer[:10],
                           "foreign_author", "commit %s" % foreign[:10], "nothing was pushed", "Compound-Harness:")
        self.assertNotIn("OVERRIDDEN", proc.stderr)
        self.assertNotIn(self.HELPER, proc.stderr, "the identity itself is not printed by default")
        self.assertFalse(self.remote_has("refs/heads/attrib"))
        range_proc = self.run_gate(["range", "origin/main..attrib"])
        self.assertEqual(range_proc.returncode, 1, range_proc.stderr)
        self.assertIn("commit %s" % trailer[:10], range_proc.stderr)

    def test_the_suggested_fix_makes_the_push_pass(self):
        self.branch("fixme")
        self.write("a.txt", "clean\n")
        self.git(["add", "a.txt"])
        self.git(["commit", "-q", "--no-verify", "-m", "fix: a\n\nCo-authored-by: Helper <%s>" % self.HELPER,
                  "--author", "Helper <%s>" % self.HELPER])
        self.assertRefused(self.push("fixme"), "foreign_author")
        self.git(["commit", "-q", "--amend", "--reset-author", "-m", "fix: a\n\nCompound-Harness: claude_code"])
        self.assertAccepted(self.push("fixme"))

    def test_commits_already_on_the_remote_are_not_checked_again(self):
        # Historical attribution (or a web merge made by the hosting service) is on the remote
        # already; a later clean push on top of it is accepted.
        self.git(["checkout", "-q", "main"])
        self.write("old.txt", "old\n")
        self.git(["add", "old.txt"])
        hosted = dict(self.env, GIT_COMMITTER_NAME="Hosting", GIT_COMMITTER_EMAIL="noreply@hosting.invalid")
        self.git(["commit", "-q", "--no-verify", "-m", "Merge pull request #1\n\nCo-authored-by: Helper <%s>"
                  % self.HELPER], env=hosted)
        self.assertAccepted(self.git(["push", "--no-verify", "origin", "main"], check=False))
        self.commit({"new.txt": "clean\n"}, message="clean change on top")
        self.assertAccepted(self.push("main"))

    def test_archive_ref_keeps_old_commits_but_a_branch_push_reaching_them_is_refused(self):
        self.branch("old-agent-work")
        self.write("a.txt", "clean\n")
        self.git(["add", "a.txt"])
        self.git(["commit", "-q", "--no-verify", "-m", "old: a\n\nCo-authored-by: Helper <%s>" % self.HELPER,
                  "--author", "Helper <%s>" % self.HELPER])
        old = self.git(["rev-parse", "HEAD"]).stdout.strip()
        refused = self.push("old-agent-work")
        self.assertRefused(refused, "foreign_author", "refs/backups/")
        self.assertAccepted(self.push("old-agent-work:refs/backups/20261116/old-agent-work"))
        self.assertTrue(self.remote_has("refs/backups/20261116/old-agent-work"))
        # The archived commit is on the remote but not in the remote-tracking refs, so a branch
        # that later reaches it is checked again.
        self.git(["checkout", "-q", "main"])
        self.git(["merge", "-q", "--no-ff", "-m", "Merge the archived work", "old-agent-work"])
        self.assertRefused(self.push("main"), "commit %s" % old[:10], "foreign_author")

    def test_archive_ref_still_runs_the_leak_rules(self):
        self.branch("old-leak")
        self.commit({"a.txt": "%s\n" % TOKEN}, verify=False)
        self.assertRefused(self.push("old-leak:refs/backups/20261116/old-leak"), "fake_product")

    def test_attribution_refuses_at_commit_even_without_rules(self):
        os.rename(self.rules, self.rules + ".hidden")
        try:
            before = self.head_count()
            proc = self.try_commit("fix: a change\n\nCo-authored-by: Helper <%s>" % self.HELPER)
            self.assertCommitRefused(proc, "co_author_trailer", before)
        finally:
            os.rename(self.rules + ".hidden", self.rules)

    def test_no_configured_identity_refuses_the_push(self):
        self.branch("noid")
        self.commit({"a.txt": "clean\n"}, message="clean change")
        env = dict(self.env, GIT_CONFIG_GLOBAL=os.path.join(self.tmp, "empty.gitconfig"))
        open(env["GIT_CONFIG_GLOBAL"], "w").close()
        self.assertRefused(self.git(["push", "origin", "noid"], env=env, check=False), "no identity configured")


class CommandTest(GateRepo):

    def test_range_mode_matches_the_hook(self):
        self.branch("r")
        self.commit({"a.txt": "clean\n"})
        self.assertEqual(self.run_gate(["range", "origin/main..HEAD"]).returncode, 0)
        self.commit({"b.txt": "%s\n" % TOKEN}, verify=False)
        proc = self.run_gate(["range", "origin/main..HEAD"])
        self.assertEqual(proc.returncode, 1)
        self.assertIn("fake_product", proc.stderr)

    def test_status_reports_an_installed_gate(self):
        proc = self.run_gate(["status"])
        self.assertEqual(proc.returncode, 0, proc.stdout)
        self.assertIn("points at it", proc.stdout)
        self.assertIn("3 verify, 1 keep, 1 extra path prefixes", proc.stdout)

    def test_install_refuses_to_overwrite_a_foreign_hook(self):
        hooks = self.git(["rev-parse", "--path-format=absolute", "--git-common-dir"]).stdout.strip() + "/hooks"
        with open(os.path.join(hooks, "pre-commit"), "w") as handle:
            handle.write("#!/bin/sh\nexit 0\n")
        proc = self.run_gate(["install", "--repo", self.work])
        self.assertEqual(proc.returncode, 2)
        self.assertIn("not a leak-gate hook", proc.stderr)

    def test_worktrees_share_the_installed_gate(self):
        tree = os.path.join(self.tmp, "tree")
        self.git(["worktree", "add", "-q", "-b", "wt", tree, "origin/main"])
        with open(os.path.join(tree, "a.txt"), "w") as handle:
            handle.write("%s\n" % TOKEN)
        self.git(["add", "a.txt"], cwd=tree)
        self.git(["commit", "-q", "--no-verify", "-m", "x"], cwd=tree)
        self.assertRefused(self.git(["push", "origin", "wt"], cwd=tree, check=False), "fake_product")


if __name__ == "__main__":
    unittest.main(verbosity=1)
