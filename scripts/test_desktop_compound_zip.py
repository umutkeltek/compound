"""Black-box snapshot regressions; every mutation stays in disposable fixtures."""
import importlib.util
import json
import os
from pathlib import Path
import stat
import subprocess
import sys
import time
import tempfile
import unittest
from unittest import mock
import zipfile

SCRIPTS = Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location("snapshot", SCRIPTS / "desktop_compound_zip.py")
snapshot = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(snapshot)


class SnapshotTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="snapshot-regression-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.repo = self.root / "source repo"
        self.desktop = self.root / "output desktop"
        self.repo.mkdir()
        self.env = os.environ.copy()
        self.env.update(GIT_CONFIG_GLOBAL="/dev/null", GIT_CONFIG_NOSYSTEM="1",
                        GIT_OPTIONAL_LOCKS="0")
        for key in ("GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR",
                    "COMPOUND_KEEP_MEMORY_PROJECTS", "COMPOUND_STAGE_NAME", "COMPOUND_ZIP_NAME"):
            self.env.pop(key, None)
        self.git("init", "-q")
        self.write(".gitignore", "docs_private/\n.compound-private/\ncompound\n.env\n")
        self.write("README.md", "source review\n")
        self.write("internal/example.go", "package example\n")
        self.write("scripts/run.sh", "#!/bin/sh\nexit 0\n")
        (self.repo / "scripts/run.sh").chmod(0o755)
        self.write("testdata/compiled-fixture", b"\x7fELFfixture")
        self.git("add", ".gitignore", "README.md", "internal/example.go", "scripts/run.sh",
                 "testdata/compiled-fixture")
        self.git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
                 "commit", "-qm", "fixture")

    def git(self, *args):
        return subprocess.run(["git", "-C", str(self.repo), *args], env=self.env,
                              check=True, capture_output=True)

    def write(self, name, content):
        path = self.repo / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(content if isinstance(content, bytes) else content.encode())
        return path

    def execute(self, *args, env=None):
        return subprocess.run(["bash", str(SCRIPTS / "desktop-compound-zip.sh"),
                               "--repo", str(self.repo), "--desktop", str(self.desktop), *args],
                              capture_output=True, text=True, env=env or self.env, timeout=60)

    def archive(self):
        return zipfile.ZipFile(self.desktop / "compound.zip")

    def old_outputs(self):
        stage = self.desktop / "compound"
        stage.mkdir(parents=True)
        (stage / "old.txt").write_text("old usable stage")
        with zipfile.ZipFile(self.desktop / "compound.zip", "w") as archive:
            archive.writestr("compound/old.txt", "old usable archive")
        return (self.desktop / "compound.zip").read_bytes()

    def assert_old_intact(self, original):
        self.assertEqual((self.desktop / "compound.zip").read_bytes(), original)
        self.assertEqual((self.desktop / "compound/old.txt").read_text(), "old usable stage")
        self.assertFalse(list(self.desktop.glob(".compound-snapshot-*")))
        self.assertFalse((self.desktop / ".compound-desktop-snapshot.lock").exists())

    def test_review_retains_context_and_documents_bulk_omissions(self):
        self.write("docs_private/plan/CURRENT.md", "current private plan")
        self.write("docs_private/plan/reports/readout.json", '{"verdict":"waiting"}')
        self.write("docs_private/plan/reports/small.log", "useful bounded failure evidence")
        self.write("docs_private/plan/reports/qa/run.xcresult/Data/raw-result", "opaque test bundle")
        self.write("docs_private/plan/reports/qa/test.app/Contents/app", "compiled application")
        self.write("docs_private/plan/reports/huge.log", b"x" * 1_000_001)
        self.write("docs_private/plan/reports/candidate", b"\xcf\xfa\xed\xfefake binary")
        self.write("docs_private/plan/reports/recovery.tar.gz", b"archive")
        self.write("docs_private/research/state/large.json", "generated index")
        self.write("docs_private/plan/reports/check.presubmit-fixture/live.log", "generated gate output")
        self.write("docs_private/portfolio/execution/run.json", "generated execution")
        self.write("compound", b"\x7fELFroot binary")
        self.write(".env", "SYNTHETIC_TOKEN=fixture-only")
        self.write(".env.example", "SYNTHETIC_TOKEN=")
        self.write("node_modules/example/index.js", "dependency")
        self.write("memory/compound/notes.md", "kept memory")
        self.write("memory/other/notes.md", "external clone")
        result = self.execute()
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        with self.archive() as archive:
            names = set(archive.namelist())
            for name in ("README.md", "internal/example.go", "docs_private/plan/CURRENT.md",
                         "docs_private/plan/reports/readout.json", "docs_private/plan/reports/small.log",
                         "testdata/compiled-fixture", ".env.example", "memory/compound/notes.md"):
                self.assertIn("compound/" + name, names)
            for name in ("compound", ".git/HEAD", ".env", "node_modules/example/index.js",
                         "docs_private/plan/reports/candidate", "docs_private/plan/reports/huge.log",
                         "docs_private/plan/reports/recovery.tar.gz",
                         "docs_private/research/state/large.json", "memory/other/notes.md",
                         "docs_private/plan/reports/check.presubmit-fixture/live.log",
                         "docs_private/portfolio/execution/run.json",
                         "docs_private/plan/reports/qa/run.xcresult/Data/raw-result",
                         "docs_private/plan/reports/qa/test.app/Contents/app"):
                self.assertNotIn("compound/" + name, names)
            manifest = json.loads(archive.read("compound/SNAPSHOT-MANIFEST.json"))
            omitted = {x["path"]: x["reason"] for x in manifest["omitted_staged_files"]}
            self.assertIn("docs_private/plan/reports/candidate", omitted)
            self.assertIn("docs_private/plan/reports/huge.log", omitted)
            self.assertEqual(manifest["budget_bytes"], 450_000_000)
            self.assertIsNone(archive.testzip())
        self.assertTrue((self.repo / "docs_private/plan/reports/candidate").exists())

    def test_budget_preserves_old_outputs_and_uses_decimal_mb(self):
        original = self.old_outputs()
        self.write("payload.dat", b"random: " + os.urandom(1_020_000))
        result = self.execute("--max-zip-mb", "1")
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn("budget", result.stderr)
        self.assert_old_intact(original)

    def test_dry_run_does_not_create_destination(self):
        self.assertFalse(self.desktop.exists())
        result = self.execute("--dry-run")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(self.desktop.exists())

    def test_copy_failure_preserves_old_outputs(self):
        original = self.old_outputs()
        fake = self.root / "fake-tools"
        fake.mkdir()
        tool = fake / "rsync"
        tool.write_text("#!/bin/sh\nexit 23\n")
        tool.chmod(0o755)
        env = dict(self.env, PATH=str(fake) + os.pathsep + self.env["PATH"])
        result = self.execute(env=env)
        self.assertNotEqual(result.returncode, 0)
        self.assert_old_intact(original)

    def test_success_replaces_old_outputs_after_validation(self):
        self.old_outputs()
        result = self.execute()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse((self.desktop / "compound/old.txt").exists())
        with self.archive() as archive:
            self.assertIsNone(archive.testzip())
            self.assertIn("compound/README.md", archive.namelist())

    def test_symlinks_are_links_and_modes_survive(self):
        outside = self.root / "outside"
        outside.mkdir()
        (outside / "not-for-snapshot.txt").write_text("outside sentinel")
        (self.repo / "external-dir").symlink_to(outside, target_is_directory=True)
        (self.repo / "external-file").symlink_to(outside / "not-for-snapshot.txt")
        os.utime(self.repo / "README.md", (1, 1))
        result = self.execute()
        self.assertEqual(result.returncode, 0, result.stderr)
        with self.archive() as archive:
            self.assertFalse(any("not-for-snapshot.txt" in n for n in archive.namelist()))
            link = archive.getinfo("compound/external-file")
            self.assertTrue(stat.S_ISLNK(link.external_attr >> 16))
            self.assertEqual(archive.read(link), os.fsencode(outside / "not-for-snapshot.txt"))
            self.assertEqual(archive.getinfo("compound/scripts/run.sh").external_attr >> 16 & 0o777, 0o755)

    def test_full_local_opt_in_keeps_binary_and_git(self):
        self.write("compound", b"\x7fELFfixture binary")
        self.write("docs_private/plan/reports/retained.zip", b"fixture archive")
        self.write(".claude/runtime.txt", "runtime state")
        result = self.execute("--full-local", "--max-zip-mb", "0")
        self.assertEqual(result.returncode, 0, result.stderr)
        with self.archive() as archive:
            names = archive.namelist()
            self.assertIn("compound/.git/HEAD", names)
            self.assertIn("compound/compound", names)
            self.assertIn("compound/docs_private/plan/reports/retained.zip", names)
            self.assertNotIn("compound/.claude/runtime.txt", names)

    def test_private_and_dependency_options_are_explicit(self):
        self.write("docs_private/notes.md", "private context")
        self.write("node_modules/example/native.node", b"\x7fELFfixture addon")
        result = self.execute("--upload-safe")
        self.assertEqual(result.returncode, 0, result.stderr)
        with self.archive() as archive:
            self.assertNotIn("compound/docs_private/notes.md", archive.namelist())
        result = self.execute("--include-private", "--upload-safe", "--include-node-modules")
        self.assertEqual(result.returncode, 0, result.stderr)
        with self.archive() as archive:
            self.assertIn("compound/docs_private/notes.md", archive.namelist())
            self.assertIn("compound/node_modules/example/native.node", archive.namelist())

    def test_custom_rsync_excludes_and_delta(self):
        self.write("docs/notes with spaces.md", "explicitly omitted")
        self.write("memory/compound/context.md", "external memory")
        self.write(".compound-private/skills/context.md", "overlay")
        result = self.execute("--delta", "--exclude", "/docs/notes with spaces.md")
        self.assertEqual(result.returncode, 0, result.stderr)
        with self.archive() as archive:
            names = archive.namelist()
            self.assertNotIn("compound/docs/notes with spaces.md", names)
            self.assertNotIn("compound/memory/compound/context.md", names)
            self.assertNotIn("compound/.compound-private/skills/context.md", names)
            self.assertIn("compound/internal/example.go", names)

    def test_invalid_destinations_do_not_touch_source(self):
        for options in (("--desktop", str(self.root), "--name", self.repo.name),
                        ("--desktop", str(self.repo / "output")),
                        ("--name", "../escape"), ("--name", "same", "--zip", "same"),
                        ("--max-zip-mb", "-1"),
                        ("--name", ".compound-desktop-snapshot.lock"),
                        ("--zip", ".compound-desktop-snapshot.lock"),
                        ("--name", ".compound-snapshot-reserved")):
            with self.subTest(options=options):
                result = self.execute(*options)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual((self.repo / "README.md").read_text(), "source review\n")
        self.assertFalse(self.desktop.exists())
        self.desktop.mkdir()
        (self.desktop / "compound").symlink_to(self.repo, target_is_directory=True)
        result = self.execute()
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue((self.desktop / "compound").is_symlink())

    def test_existing_lock_is_not_stolen(self):
        original = self.old_outputs()
        lock = self.desktop / ".compound-desktop-snapshot.lock"
        lock.mkdir()
        (lock / "owner.json").write_text('{"pid":123}')
        result = self.execute()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("another snapshot", result.stderr)
        self.assertEqual((self.desktop / "compound.zip").read_bytes(), original)
        self.assertTrue((lock / "owner.json").exists())


    def test_special_files_are_not_opened_or_archived(self):
        os.mkfifo(self.repo / "runtime.pipe")
        result = self.execute()
        self.assertEqual(result.returncode, 0, result.stderr)
        with self.archive() as archive:
            self.assertNotIn("compound/runtime.pipe", archive.namelist())
            manifest = json.loads(archive.read("compound/SNAPSHOT-MANIFEST.json"))
            self.assertTrue(manifest["special_files_excluded"])

    def test_generated_metadata_does_not_follow_source_symlinks(self):
        outside = self.root / "external-sentinel.txt"
        outside.write_text("untouched external content")
        for name in ("AUDIT-NOTES.md", "SNAPSHOT-MANIFEST.json"):
            (self.repo / name).symlink_to(outside)
        result = self.execute()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(outside.read_text(), "untouched external content")
        for name in ("AUDIT-NOTES.md", "SNAPSHOT-MANIFEST.json"):
            self.assertTrue((self.repo / name).is_symlink())
        with self.archive() as archive:
            for name in ("AUDIT-NOTES.md", "SNAPSHOT-MANIFEST.json"):
                self.assertFalse(stat.S_ISLNK(archive.getinfo("compound/" + name).external_attr >> 16))

    def test_second_opinion_metadata_never_follows_selected_links(self):
        outside = self.root / "external-review.json"
        outside.write_text("untouched external review")
        for name in ("REVIEW-MANIFEST.json", "REVIEW-START-HERE.md", "RECENT-COMMITS.txt"):
            (self.repo / name).symlink_to(outside)
        result = self.execute("--second-opinion")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(outside.read_text(), "untouched external review")
        with self.archive() as archive:
            for name in ("REVIEW-MANIFEST.json", "REVIEW-START-HERE.md", "RECENT-COMMITS.txt"):
                self.assertFalse(stat.S_ISLNK(archive.getinfo("compound/" + name).external_attr >> 16))
            manifest = json.loads(archive.read("compound/REVIEW-MANIFEST.json"))
            self.assertIn("README.md", {entry["path"] for entry in manifest["files"]})

    def test_terminated_copy_cleans_up_without_replacing_outputs(self):
        original = self.old_outputs()
        fake = self.root / "slow-tools"
        fake.mkdir()
        ready = self.root / "copy-ready"
        tool = fake / "rsync"
        tool.write_text("#!" + sys.executable + "\nfrom pathlib import Path\nimport time\nPath(" +
                        repr(str(ready)) + ").write_text('ready')\ntime.sleep(30)\n")
        tool.chmod(0o755)
        env = dict(self.env, PATH=str(fake) + os.pathsep + self.env["PATH"])
        process = subprocess.Popen(["bash", str(SCRIPTS / "desktop-compound-zip.sh"),
                                    "--repo", str(self.repo), "--desktop", str(self.desktop)],
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env)
        try:
            deadline = time.monotonic() + 20
            while not ready.exists() and process.poll() is None and time.monotonic() < deadline:
                time.sleep(0.05)
            self.assertTrue(ready.exists(), "copy process did not reach the injected wait")
            process.terminate()
            process.communicate(timeout=15)
            self.assertNotEqual(process.returncode, 0)
        finally:
            if process.poll() is None:
                process.kill()
                process.communicate()
        self.assert_old_intact(original)


    def test_hup_during_publication_keeps_a_complete_output_pair(self):
        self.old_outputs()
        code = """
import os, signal, sys
sys.path.insert(0, %r)
import desktop_compound_zip as snapshot
replace = os.replace
def interrupt_after_backup(source, destination):
    replace(source, destination)
    if str(destination).endswith('/previous-stage'):
        os.kill(os.getpid(), signal.SIGHUP)
snapshot.os.replace = interrupt_after_backup
raise SystemExit(snapshot.main())
""" % str(SCRIPTS)
        result = subprocess.run([sys.executable, "-c", code, "--repo", str(self.repo),
                                 "--desktop", str(self.desktop)],
                                capture_output=True, text=True, env=self.env, timeout=60)
        self.assertNotEqual(result.returncode, 0)
        self.assertTrue((self.desktop / "compound/README.md").is_file(), result.stderr)
        with self.archive() as archive:
            self.assertIsNone(archive.testzip())
            self.assertEqual(archive.read("compound/README.md"), b"source review\n")
        self.assertFalse((self.desktop / ".compound-desktop-snapshot.lock").exists())

    def test_publish_failure_restores_previous_stage_and_zip(self):
        original = self.old_outputs()
        candidate = self.root / "candidate"
        candidate.mkdir()
        (candidate / "new.txt").write_text("candidate")
        archive = self.root / "candidate.zip"
        archive.write_bytes(b"new archive")
        target = self.desktop / "compound.zip"
        actual_replace = os.replace

        def fail_final_replace(source, destination):
            if destination == target:
                raise OSError("injected publication failure")
            return actual_replace(source, destination)

        with mock.patch.object(snapshot.os, "replace", side_effect=fail_final_replace):
            with self.assertRaisesRegex(OSError, "injected"):
                snapshot.publish(candidate, archive, self.desktop / "compound",
                                 target, self.root / "backup")
        self.assertEqual(target.read_bytes(), original)
        self.assertEqual((self.desktop / "compound/old.txt").read_text(), "old usable stage")


if __name__ == "__main__":
    unittest.main()
