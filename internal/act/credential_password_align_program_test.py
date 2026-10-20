"""Synthetic protocol and actual Linux/PostgreSQL fixtures for the closed owner."""
import copy
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import unittest


ROOT = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("alignment_fixture", ROOT / "credential_verify_program.py")
owner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(owner)
exec(compile((ROOT / "credential_password_align_program.py").read_bytes(), "credential_password_align_program.py", "exec"), owner.__dict__)


class AlignmentFormatTests(unittest.TestCase):
    def test_mixed_or_revision_drift_never_classifies_as_postimage(self):
        before = {"roles": [{"oid": 1, "xmin": "1", "ctid": "(1,1)", "verifier": "old-a", "attributes": {}},
                            {"oid": 2, "xmin": "1", "ctid": "(1,2)", "verifier": "old-m", "attributes": {}}]}
        after = copy.deepcopy(before)
        for row in after["roles"]:
            row["verifier"] = "new"
            row["xmin"] = "2"
        self.assertEqual(owner.alignment_classify(before, before, after), "old_old")
        self.assertEqual(owner.alignment_classify(after, before, after), "new_new")
        mixed = copy.deepcopy(after)
        mixed["roles"][0] = before["roles"][0]
        with self.assertRaises(owner.Refusal):
            owner.alignment_classify(mixed, before, after)
        drift = copy.deepcopy(after)
        drift["roles"][0]["xmin"] = "3"
        with self.assertRaises(owner.Refusal):
            owner.alignment_classify(drift, before, after)

    def test_scram_format_rejects_plaintext_sql_and_invalid_digests(self):
        for value in ("plaintext", "SCRAM-SHA-256$4096:abc$bad:bad", "x';SELECT 1;--", None):
            with self.assertRaises(owner.Refusal):
                owner.alignment_scram(value)


@unittest.skipUnless(os.environ.get("COMPOUND_ALIGNMENT_LINUX_PG17") == "1", "requires owned disposable Linux/root PostgreSQL17 fixture")
class LinuxPostgresAlignmentTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        r = {"ssh": {"client_path": "/usr/lib/postgresql/17/bin/psql"}}
        result = subprocess.run(owner.client_argv(r, "postgres", True), env=owner.clean_env(),
            input=b"SELECT current_setting('hba_file');\n", capture_output=True, check=True)
        hba = Path(result.stdout.decode("ascii").strip())
        hba.write_text("local all all peer\nhost all all 127.0.0.1/32 scram-sha-256\nhost all all ::1/128 scram-sha-256\n")
        subprocess.run(owner.client_argv(r, "postgres", True), env=owner.clean_env(),
            input=b"SELECT pg_reload_conf();\n", capture_output=True, check=True)

    def query(self, sql):
        r = {"ssh": {"client_path": "/usr/lib/postgresql/17/bin/psql"}}
        result = subprocess.run(owner.client_argv(r, "postgres", True), env=owner.clean_env(),
                                input=("SET log_statement='none'; SET log_min_error_statement='panic'; SET client_min_messages='warning';\n" + sql).encode("ascii"), capture_output=True, timeout=15)
        self.assertEqual(result.returncode, 0)
        self.assertFalse(result.stderr)
        return result.stdout

    def setUp(self):
        self.assertEqual(os.geteuid(), 0)
        self.query("DROP DATABASE IF EXISTS repair_example; DROP ROLE IF EXISTS repair_app; DROP ROLE IF EXISTS repair_migrator;")
        self.query("CREATE ROLE repair_app LOGIN PASSWORD 'synthetic-old-app'; CREATE ROLE repair_migrator LOGIN PASSWORD 'synthetic-old-migrator';")
        self.query("CREATE DATABASE repair_example OWNER repair_migrator CONNECTION LIMIT 0;")
        self.query("REVOKE CONNECT ON DATABASE repair_example FROM PUBLIC; REVOKE CONNECT ON DATABASE repair_example FROM repair_app;")
        self.r = {"ssh": {"client_path": "/usr/lib/postgresql/17/bin/psql"}, "roles": [{"name": "repair_app"}, {"name": "repair_migrator"}],
                  "database": {"name": "repair_example", "allow_connections": True}}
        self.processes = owner.Processes(time.monotonic() + 30)
        self.root = Path("/root/compound-alignment-fixture")
        self.root.mkdir(mode=0o700, exist_ok=True)
        self.directory = Path(tempfile.mkdtemp(dir=self.root))
        self.r["file"] = {"path": str(self.directory / "original.env")}
        (self.directory / "original.env").write_text("SYNTHETIC=fixture\n")
        (self.directory / "original.env").chmod(0o600)

    def tearDown(self):
        self.assertTrue(self.processes.cleanup())

    def image(self):
        session = owner.AlignmentSession(self.r, self.processes)
        try:
            value = session.catalog(self.r)
            owner.alignment_closed_catalog(value, self.r)
            return value
        finally:
            session.close()

    def test_native_scram_duplex_transaction_and_two_actual_authentications(self):
        before = self.image()
        session = owner.AlignmentSession(self.r, self.processes)
        passwords = ["synthetic-canary-app", "synthetic-canary-migrator"]
        try:
            session.align(self.r, passwords)
            after = session.catalog(self.r)
            self.assertEqual(owner.alignment_public_catalog(before, False), owner.alignment_public_catalog(after, False))
            for index in range(2):
                owner.alignment_scram(after["roles"][index]["verifier"])
                self.assertNotIn(passwords[index], json.dumps(after))
            session.commit()
        finally:
            session.close()
        for index, role in enumerate(self.r["roles"]):
            result = subprocess.run(owner.client_argv(self.r, role["name"]), env=owner.clean_env(passwords[index]),
                                    input=b"SELECT CURRENT_USER;\n", capture_output=True, timeout=10)
            self.assertEqual(result.returncode, 0)
            self.assertEqual(result.stdout, (role["name"] + "\n").encode())
            result = subprocess.run(owner.client_argv(self.r, role["name"]), env=owner.clean_env("synthetic-wrong"),
                                    input=b"SELECT 1;\n", capture_output=True, timeout=10)
            self.assertEqual(result.returncode, 2)
            self.assertFalse(result.stdout)
            self.assertNotIn(passwords[index].encode(), result.stderr)
        self.assertEqual(owner.alignment_classify(self.image(), before, after), "new_new")

    def test_uncommitted_two_password_changes_roll_back_on_client_death(self):
        before = self.image()
        session = owner.AlignmentSession(self.r, self.processes)
        session.align(self.r, ["synthetic-never-committed-a", "synthetic-never-committed-m"])
        session.close()
        self.assertEqual(self.image(), before)

    def test_restore_is_one_transaction_and_preserves_all_nonpassword_catalogs(self):
        before = self.image()
        session = owner.AlignmentSession(self.r, self.processes)
        session.align(self.r, ["synthetic-new-a", "synthetic-new-m"])
        after = session.catalog(self.r)
        session.commit()
        session.close()
        self.assertEqual(owner.alignment_classify(self.image(), before, after), "new_new")
        session = owner.AlignmentSession(self.r, self.processes)
        session.restore(self.r, before)
        session.commit()
        session.close()
        restored = self.image()
        self.assertEqual(owner.alignment_public_catalog(restored, False), owner.alignment_public_catalog(before, False))
        self.assertEqual([x["verifier"] for x in restored["roles"]], [x["verifier"] for x in before["roles"]])

    def test_private_custody_is_exclusive_durable_and_rejects_hardlinks_symlinks(self):
        custody = owner.AlignmentCustody(self.r, "act_0123456789ab", True)
        try:
            image = self.image()
            custody.put("PREIMAGE.json", image)
            self.assertEqual(custody.get("PREIMAGE.json"), image)
            with self.assertRaises(FileExistsError):
                custody.put("PREIMAGE.json", image)
            namespace = self.directory / owner.ALIGN_NAMESPACE / "act_0123456789ab"
            with self.assertRaises(owner.Refusal):
                owner.AlignmentCustody(self.r, "act_1123456789ab", True)
            os.link(namespace / "PREIMAGE.json", namespace / "alias")
            with self.assertRaises(owner.Refusal):
                custody.get("PREIMAGE.json")
            os.unlink(namespace / "alias")
            os.symlink(namespace / "PREIMAGE.json", namespace / "POSTIMAGE.json")
            with self.assertRaises(OSError):
                custody.get("POSTIMAGE.json")
        finally:
            custody.close()


if __name__ == "__main__":
    unittest.main()
