#!/usr/bin/env python3
"""Tests for collect.py with fake command output. Synthetic names and numbers only.

Run: python3 -I scripts/inventory/test_collect.py
"""

import contextlib
import importlib.util
import io
import json
import os
import stat
import tempfile
import unittest
from datetime import datetime, timedelta, timezone
from pathlib import Path

HERE = Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location("inventory_collect", HERE / "collect.py")
collect = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(collect)

CLOCK = datetime(2031, 4, 5, 6, 7, 8, tzinfo=timezone.utc)
ACCOUNT = "210987654321"
R = collect.Result


def now():
    return CLOCK


class FakeRunner:
    """Answers commands by the first rule whose words all appear in the command."""

    def __init__(self, rules):
        self.rules = rules
        self.calls = []

    def __call__(self, cmd, timeout, env=None):
        self.calls.append((list(cmd), timeout, dict(env or {})))
        text = " ".join(cmd)
        for words, answer in self.rules:
            if all(word in text for word in words):
                if isinstance(answer, Exception):
                    raise answer
                return answer
        return R(127, "", f"fake: no rule for {text}")


def observe(rows, runner=None, http=None, only=None, env=None):
    return collect.collect({"sources": rows}, runner=runner or FakeRunner([]), http=http, now=now,
                           only=only, timeout_s=5, env=env or {})


def by_kind(obs, kind):
    return [r for r in obs["resources"] if r["kind"] == kind]


def source(obs, sid):
    return next(s for s in obs["sources"] if s["id"] == sid)


AWS_IDENTITY = R(0, json.dumps({"Account": ACCOUNT, "Arn": f"arn:aws:iam::{ACCOUNT}:user/example-user",
                                "UserId": "AIDAEXAMPLE0000000001"}), "")
AWS_LISTING = R(0, json.dumps({"ResourceTagMappingList": [
    {"ResourceARN": f"arn:aws:ecs:eu-example-1:{ACCOUNT}:service/example-svc"},
    {"ResourceARN": f"arn:aws:ecs:eu-example-1:{ACCOUNT}:task/example-task"},
    {"ResourceARN": f"arn:aws:s3:::example-bucket"}]}), "")
AWS_ROW = {"id": "aws-example", "provider": "aws", "profile": "example-profile", "label": "Example account"}


class AwsTests(unittest.TestCase):
    def runner(self, identity=AWS_IDENTITY, listing=AWS_LISTING):
        return FakeRunner([(["sts", "get-caller-identity"], identity),
                           (["configure", "get", "region"], R(0, "eu-example-1\n", "")),
                           (["resourcegroupstaggingapi", "get-resources"], listing)])

    def test_account_resource_is_hashed_and_keeps_only_the_tail(self):
        runner = self.runner()
        obs = observe([AWS_ROW], runner)
        (account,) = by_kind(obs, "account")
        self.assertEqual(account["id"], "aws:account:" + collect.short_hash(ACCOUNT))
        self.assertEqual(account["label"], "Example account")
        self.assertEqual(account["facts"]["account_tail"]["value"], "4321")
        self.assertEqual(account["facts"]["identity_ok"]["value"], True)
        self.assertEqual(account["facts"]["resources"]["value"], 3)
        self.assertIn("ecs 2", account["facts"]["resources"]["detail"])
        self.assertNotIn(ACCOUNT, json.dumps(obs))
        listing_cmd = next(c for c, _, _ in runner.calls if "resourcegroupstaggingapi" in c)
        self.assertIn("eu-example-1", listing_cmd)
        self.assertIn("--max-items", listing_cmd)
        self.assertEqual(source(obs, "aws-example")["state"], "ok")

    def test_paid_and_owner_only_facts_carry_a_state_and_no_number(self):
        account = by_kind(observe([AWS_ROW], self.runner()), "account")[0]
        self.assertEqual(account["facts"]["cost_month_to_date"]["state"], "costs_money")
        self.assertIsNone(account["facts"]["cost_month_to_date"]["value"])
        self.assertEqual(account["facts"]["credit_left"]["state"], "needs_owner")
        self.assertIsNone(account["facts"]["credit_left"]["value"])

    def test_cost_explorer_is_never_called(self):
        runner = self.runner()
        observe([AWS_ROW], runner)
        self.assertFalse([c for c, _, _ in runner.calls if "ce" in c])

    def test_expired_sign_in_is_needs_sign_in(self):
        expired = R(254, "", "An error occurred (ExpiredToken) when calling the GetCallerIdentity operation: "
                             "The security token included in the request is expired")
        obs = observe([AWS_ROW], self.runner(identity=expired))
        self.assertEqual(source(obs, "aws-example")["state"], "needs_sign_in")
        self.assertEqual(obs["resources"], [])

    def test_denied_listing_keeps_the_account_and_marks_the_source(self):
        denied = R(254, "", "An error occurred (AccessDeniedException) when calling the GetResources operation")
        obs = observe([AWS_ROW], self.runner(listing=denied))
        (account,) = by_kind(obs, "account")
        self.assertEqual(account["facts"]["resources"]["state"], "needs_owner")
        self.assertIsNone(account["facts"]["resources"]["value"])
        self.assertEqual(source(obs, "aws-example")["state"], "needs_owner")

    def test_capped_listing_says_so(self):
        capped = R(0, json.dumps({"ResourceTagMappingList": [], "NextToken": "next-page"}), "")
        account = by_kind(observe([AWS_ROW], self.runner(listing=capped)), "account")[0]
        self.assertIn("capped at 2000", account["facts"]["resources"]["detail"])

    def test_row_region_skips_the_config_lookup(self):
        runner = self.runner()
        observe([dict(AWS_ROW, region="ap-example-2")], runner)
        self.assertFalse([c for c, _, _ in runner.calls if "configure" in c])
        listing_cmd = next(c for c, _, _ in runner.calls if "resourcegroupstaggingapi" in c)
        self.assertIn("ap-example-2", listing_cmd)


GIB = 2 ** 30
PVE = [
    {"type": "node", "node": "node-a", "status": "online", "cpu": 0.105, "maxcpu": 8,
     "mem": 48 * GIB, "maxmem": 64 * GIB, "disk": 25 * GIB, "maxdisk": 100 * GIB},
    {"type": "qemu", "vmid": 101, "name": "vm-alpha", "node": "node-a", "status": "running",
     "cpu": 0.5, "mem": 3 * GIB, "maxmem": 4 * GIB, "disk": 0, "maxdisk": 32 * GIB},
    {"type": "lxc", "vmid": 102, "name": "ct-beta", "node": "node-a", "status": "running",
     "cpu": 0.01, "mem": 1 * GIB, "maxmem": 2 * GIB, "disk": 1 * GIB, "maxdisk": 8 * GIB},
    {"type": "qemu", "vmid": 103, "name": "vm-gamma", "node": "node-a", "status": "stopped",
     "cpu": 0, "mem": 0, "maxmem": 4 * GIB, "disk": 0, "maxdisk": 16 * GIB},
    {"type": "qemu", "vmid": 9000, "name": "tmpl", "node": "node-a", "status": "stopped", "template": 1},
    {"type": "storage", "storage": "store-a", "node": "node-a", "status": "available",
     "disk": 50 * GIB, "maxdisk": 100 * GIB},
]
PVE_ROW = {"id": "pve-example", "provider": "pve", "ssh_alias": "pve.example", "label": "Example node"}


class PveTests(unittest.TestCase):
    def observe(self, answer):
        return observe([PVE_ROW], FakeRunner([(["pve.example", "pvesh get /cluster/resources"], answer)]))

    def test_nodes_and_guests(self):
        obs = self.observe(R(0, json.dumps(PVE), ""))
        (host,) = by_kind(obs, "host")
        guests = {g["label"]: g for g in by_kind(obs, "guest")}
        self.assertEqual(sorted(guests), ["ct-beta", "vm-alpha", "vm-gamma"])
        self.assertEqual(host["label"], "Example node")
        self.assertEqual(host["facts"]["guests_running"]["value"], 2)
        self.assertEqual(host["facts"]["cpu_pct"]["value"], 10.5)
        self.assertEqual(host["facts"]["memory_pct"]["value"], 75.0)
        self.assertEqual(host["facts"]["disk_pct"]["value"], 25.0)
        self.assertEqual(host["facts"]["storage_max_pct"]["value"], 50.0)
        for guest in guests.values():
            self.assertEqual(guest["parent"], host["id"])
        self.assertIn("1 templates", source(obs, "pve-example")["detail"])

    def test_vm_disk_use_is_not_known_rather_than_zero(self):
        guests = {g["label"]: g for g in by_kind(self.observe(R(0, json.dumps(PVE), "")), "guest")}
        vm_disk = guests["vm-alpha"]["facts"]["disk_pct"]
        self.assertEqual(vm_disk["state"], "not_supported")
        self.assertIsNone(vm_disk["value"])
        self.assertEqual(guests["ct-beta"]["facts"]["disk_pct"]["value"], 12.5)
        self.assertEqual(guests["vm-alpha"]["facts"]["memory_pct"]["value"], 75.0)

    def test_stopped_guest_reports_status_only(self):
        guests = {g["label"]: g for g in by_kind(self.observe(R(0, json.dumps(PVE), "")), "guest")}
        self.assertEqual(guests["vm-gamma"]["facts"]["status"]["value"], "stopped")
        self.assertNotIn("cpu_pct", guests["vm-gamma"]["facts"])

    def test_ids_are_stable_across_runs(self):
        first = [r["id"] for r in self.observe(R(0, json.dumps(PVE), ""))["resources"]]
        second = [r["id"] for r in self.observe(R(0, json.dumps(PVE), ""))["resources"]]
        self.assertEqual(first, second)
        self.assertEqual(len(set(first)), len(first))

    def test_rejected_key_is_needs_sign_in(self):
        obs = self.observe(R(255, "", "user@pve.example: Permission denied (publickey)."))
        self.assertEqual(source(obs, "pve-example")["state"], "needs_sign_in")

    def test_timeout_is_failed_with_the_reason(self):
        obs = self.observe(collect.SourceError("failed", "ssh timed out after 5 s"))
        self.assertEqual(source(obs, "pve-example")["state"], "failed")
        self.assertIn("timed out", source(obs, "pve-example")["detail"])


LINUX_STATUS = "\n".join([
    " 10:00:00 up 46 days,  3:04,  0 users,  load average: 0.42, 0.30, 0.25",
    "@@df",
    "Filesystem     1024-blocks     Used Available Capacity Mounted on",
    "/dev/sda1         78643200 63700000  14943200      81% /",
    "@@mem",
    "MemTotal:        8000000 kB",
    "MemFree:          500000 kB",
    "MemAvailable:    2000000 kB",
    "@@stat",
    "cpu  1000 0 1000 8000 0 0 0 0 0 0",
    "cpu  1100 0 1100 8700 100 0 0 0 0 0",
    "@@end",
]) + "\n"
MAC_STATUS = "\n".join([
    "10:00  up 3 days, 2:01, 2 users, load averages: 1.50 1.40 1.30",
    "@@df",
    "Filesystem 1024-blocks Used Available Capacity Mounted on",
    "/dev/disk3s1 971350180 400000000 571350180 42% /",
    "@@mem",
    "@@stat",
    "@@end",
]) + "\n"
SSH_ROW = {"id": "vps-example", "provider": "ssh_host", "ssh_alias": "vps.example", "label": "Example server"}


class SshHostTests(unittest.TestCase):
    def observe(self, answer):
        return observe([SSH_ROW], FakeRunner([(["vps.example", "df -P /"], answer)]))

    def test_linux_host(self):
        (host,) = by_kind(self.observe(R(0, LINUX_STATUS, "")), "host")
        facts = host["facts"]
        self.assertEqual(facts["disk_pct"]["value"], 81.0)
        self.assertEqual(facts["memory_pct"]["value"], 75.0)
        self.assertEqual(facts["cpu_pct"]["value"], 20.0)  # 200 busy of 1000 ticks; iowait counts as idle
        self.assertEqual(facts["uptime_days"]["value"], 46)
        self.assertEqual(facts["load_1m"]["value"], 0.42)
        self.assertEqual(host["label"], "Example server")
        self.assertEqual(host["id"], "ssh:host:" + collect.short_hash("vps.example"))

    def test_host_without_proc_says_not_known(self):
        facts = by_kind(self.observe(R(0, MAC_STATUS, "")), "host")[0]["facts"]
        self.assertEqual(facts["disk_pct"]["value"], 42.0)
        self.assertEqual(facts["memory_pct"]["state"], "not_supported")
        self.assertIsNone(facts["memory_pct"]["value"])
        self.assertEqual(facts["cpu_pct"]["state"], "not_supported")
        self.assertEqual(facts["load_1m"]["value"], 1.5)

    def test_unreachable_host_is_failed(self):
        obs = self.observe(R(255, "", "ssh: connect to host vps.example port 22: Operation timed out"))
        self.assertEqual(source(obs, "vps-example")["state"], "failed")
        self.assertEqual(source(obs, "vps-example")["detail"], "the host did not answer")
        self.assertEqual(obs["resources"], [])


class CoreTests(unittest.TestCase):
    def test_observation_shape_and_injected_clock(self):
        obs = observe([AWS_ROW], AwsTests().runner())
        self.assertEqual(obs["schema_version"], "compound.system_inventory_observation.v0.1")
        self.assertEqual(obs["observed_at"], "2031-04-05T06:07:08Z")
        self.assertEqual(obs["collector"]["name"], "compound-inventory-collect")
        src = obs["sources"][0]
        self.assertEqual(set(src), {"id", "provider", "state", "detail", "observed_at", "duration_ms"})
        res = obs["resources"][0]
        self.assertEqual(set(res), {"id", "kind", "provider", "label", "parent", "source", "places", "links", "facts"})
        for fact in res["facts"].values():
            self.assertEqual(set(fact), {"value", "unit", "state", "detail", "observed_at"})
            self.assertEqual(fact["observed_at"], "2031-04-05T06:07:08Z")

    def test_one_failing_source_never_stops_the_others(self):
        runner = FakeRunner([(["pve.example"], R(255, "", "Could not resolve hostname pve.example")),
                             (["vps.example"], R(0, LINUX_STATUS, ""))])
        obs = observe([PVE_ROW, SSH_ROW], runner)
        self.assertEqual(source(obs, "pve-example")["state"], "failed")
        self.assertEqual(source(obs, "vps-example")["state"], "ok")
        self.assertEqual(len(by_kind(obs, "host")), 1)

    def test_reader_bug_becomes_a_failed_source(self):
        collect.READERS["broken_example"] = lambda ctx: 1 / 0
        try:
            obs = observe([{"id": "b", "provider": "broken_example"}, SSH_ROW],
                          FakeRunner([(["vps.example"], R(0, LINUX_STATUS, ""))]))
        finally:
            del collect.READERS["broken_example"]
        self.assertEqual(source(obs, "b")["state"], "failed")
        self.assertIn("ZeroDivisionError", source(obs, "b")["detail"])
        self.assertEqual(source(obs, "vps-example")["state"], "ok")

    def test_disabled_unknown_and_not_selected_sources(self):
        rows = [dict(SSH_ROW, enabled=False, note="did not answer on the last check"),
                {"id": "u", "provider": "no_such_reader"},
                dict(AWS_ROW)]
        obs = observe(rows, AwsTests().runner(), only="u")
        self.assertEqual(source(obs, "vps-example")["state"], "skipped")
        self.assertEqual(source(obs, "vps-example")["detail"], "did not answer on the last check")
        self.assertEqual(source(obs, "u")["state"], "not_supported")
        self.assertEqual(source(obs, "aws-example")["state"], "skipped")
        self.assertEqual(obs["resources"], [])

    def test_duplicate_resource_ids_are_reported_once(self):
        rows = [AWS_ROW, dict(AWS_ROW, id="aws-example-2", profile="example-profile-2")]
        obs = observe(rows, AwsTests().runner())
        self.assertEqual(len(by_kind(obs, "account")), 1)
        self.assertIn("already reported", source(obs, "aws-example-2")["detail"])

    def test_ok_fact_without_value_becomes_failed(self):
        def lying(ctx):
            return [ctx.resource("x:1", "app", "Example app", facts={
                "status": ctx.fact(None), "other": ctx.unknown("needs_owner", "owner only"),
                "odd": {"value": 5, "state": "bogus"}})]
        collect.READERS["lying_example"] = lying
        try:
            obs = observe([{"id": "l", "provider": "lying_example"}])
        finally:
            del collect.READERS["lying_example"]
        facts = obs["resources"][0]["facts"]
        self.assertEqual((facts["status"]["state"], facts["status"]["value"]), ("failed", None))
        self.assertEqual((facts["other"]["state"], facts["other"]["value"]), ("needs_owner", None))
        self.assertEqual((facts["odd"]["state"], facts["odd"]["value"]), ("failed", None))

    def test_env_unset_is_per_source(self):
        runner = FakeRunner([(["vps.example"], R(0, LINUX_STATUS, ""))])
        env = {"HTTPS_PROXY": "http://proxy.example:3128", "KEEP_ME": "1"}
        observe([dict(SSH_ROW, env_unset=["HTTPS_PROXY"])], runner, env=env)
        observe([SSH_ROW], runner, env=env)
        first_env, second_env = runner.calls[0][2], runner.calls[1][2]
        self.assertNotIn("HTTPS_PROXY", first_env)
        self.assertEqual(first_env["KEEP_ME"], "1")
        self.assertIn("HTTPS_PROXY", second_env)

    def test_row_timeout_overrides_the_default(self):
        runner = FakeRunner([(["vps.example"], R(0, LINUX_STATUS, ""))])
        observe([dict(SSH_ROW, timeout_s=42)], runner)
        self.assertEqual(runner.calls[0][1], 42.0)

    def test_example_sources_file_loads_and_every_provider_has_a_reader(self):
        doc = collect.load_sources(HERE / "sources.example.json")
        self.assertEqual(doc["schema_version"], collect.SOURCES_SCHEMA)
        for row in doc["sources"]:
            self.assertIn(row["provider"], collect.READERS, row["id"])

    def test_mask_ids(self):
        self.assertEqual(collect.mask_ids(f"arn:aws:iam::{ACCOUNT}:user/x"), "arn:aws:iam::…4321:user/x")
        self.assertEqual(collect.mask_ids("ResourceGroupsTaggingAPI"), "ResourceGroupsTaggingAPI")

    def test_proxies_come_only_from_the_given_env(self):
        self.assertEqual(collect.proxies_from_env({"HTTPS_PROXY": "http://p.example:1", "NO_PROXY": "x"}),
                         {"https": "http://p.example:1"})
        self.assertEqual(collect.proxies_from_env({}), {})


class MainTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="inventory-collect-test-")
        self.addCleanup(self.tmp.cleanup)
        self.dir = Path(self.tmp.name)
        for name in ("stdout", "stderr"):
            quiet = contextlib.redirect_stdout(io.StringIO()) if name == "stdout" else \
                contextlib.redirect_stderr(io.StringIO())
            quiet.__enter__()
            self.addCleanup(quiet.__exit__, None, None, None)

    def write_sources(self, rows):
        path = self.dir / "sources.json"
        path.write_text(json.dumps({"schema_version": collect.SOURCES_SCHEMA, "sources": rows}))
        return path

    def test_writes_a_private_file(self):
        sources = self.write_sources([SSH_ROW])
        out = self.dir / "observation.json"
        runner = FakeRunner([(["vps.example"], R(0, LINUX_STATUS, ""))])
        code = collect.main(["--sources", str(sources), "--out", str(out)], runner=runner, now=now)
        self.assertEqual(code, 0)
        self.assertEqual(stat.S_IMODE(os.stat(out).st_mode), 0o600)
        doc = json.loads(out.read_text())
        self.assertEqual(len(doc["resources"]), 1)

    def test_bad_sources_file_is_a_usage_error(self):
        sources = self.write_sources([SSH_ROW, SSH_ROW])
        code = collect.main(["--sources", str(sources), "--out", str(self.dir / "o.json")], now=now)
        self.assertEqual(code, 2)
        self.assertFalse((self.dir / "o.json").exists())

    def test_unknown_only_is_a_usage_error(self):
        sources = self.write_sources([SSH_ROW])
        code = collect.main(["--sources", str(sources), "--out", str(self.dir / "o.json"), "--only", "nope"],
                            now=now)
        self.assertEqual(code, 2)


if __name__ == "__main__":
    unittest.main()
