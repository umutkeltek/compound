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
import sys
import tempfile
import unittest
from datetime import datetime, timedelta, timezone
from pathlib import Path

HERE = Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location("inventory_collect", HERE / "collect.py")
collect = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(collect)

CLOCK = datetime(2031, 4, 5, 6, 7, 8, tzinfo=timezone.utc)
ACCOUNT = "000000004321"
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


def same_bytes(obs):
    """The file as bytes, with the one field that measures wall time (duration_ms) set to 0."""
    return json.dumps(dict(obs, sources=[dict(s, duration_ms=0) for s in obs["sources"]]))


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
        obs = self.observe(R(255, "", "pve.example: Permission denied (publickey)."))
        self.assertEqual(source(obs, "pve-example")["state"], "needs_sign_in")

    def test_timeout_is_failed_with_the_reason(self):
        obs = self.observe(collect.ToolProblem("timeout", "ssh", 5))
        self.assertEqual(source(obs, "pve-example")["state"], "failed")
        self.assertEqual(source(obs, "pve-example")["detail"], "The virtualization host did not answer within 5 seconds.")


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
    "cpu  500 0 500 4000 0 0 0 0 0 0",
    "cpu  550 0 550 4350 50 0 0 0 0 0",
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
        self.assertEqual(facts["cpu_pct"]["value"], 20.0)  # 100 busy of 500 ticks; iowait counts as idle
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
        self.assertEqual(source(obs, "vps-example")["detail"], "The host did not answer.")
        self.assertEqual(obs["resources"], [])


class CoreTests(unittest.TestCase):
    def test_observation_shape_and_injected_clock(self):
        obs = observe([AWS_ROW], AwsTests().runner())
        self.assertEqual(obs["schema_version"], "compound.system_inventory_observation.v0.2")
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
        self.assertEqual(source(obs, "b")["detail"], "The reader for this source stopped on an unexpected error.")
        self.assertNotIn("ZeroDivision", json.dumps(obs))
        self.assertEqual(source(obs, "vps-example")["state"], "ok")

    def test_disabled_unknown_and_not_selected_sources(self):
        rows = [dict(SSH_ROW, enabled=False, note="did not answer on the last check"),
                {"id": "u", "provider": "no_such_reader"},
                dict(AWS_ROW)]
        obs = observe(rows, AwsTests().runner(), only="u")
        self.assertEqual(source(obs, "vps-example")["state"], "skipped")
        self.assertEqual(source(obs, "vps-example")["detail"], "Did not answer on the last check.")
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

    def test_canonical_ids(self):
        cid = collect.canonical_id
        self.assertEqual(cid("domain", "Alpha.Example."), "domain:alpha.example")
        self.assertEqual(cid("hostname", "https://WWW.alpha.example/path"), "hostname:www.alpha.example")
        self.assertEqual(cid("mailbox", "Owner@Example.COM"), "mailbox:owner@example.com")
        self.assertEqual(cid("alias", "mailto:help@example.org"), "alias:help@example.org")
        for remote in ("git@example.com:Owner/Repo-One.git", "https://example.com/owner/repo-one",
                       "ssh://git@example.com:22/owner/repo-one.git", "example.com/owner/repo-one"):
            self.assertEqual(cid("repo", remote), "repo:example.com/owner/repo-one", remote)
        self.assertEqual(cid("repo", "local/example-folder"), "repo:local/example-folder")
        self.assertEqual(cid("repo", "/abs/path/example"), "")
        self.assertEqual(cid("host", "Example Node"), "host:example-node")
        self.assertEqual(cid("store_app", "Apple/com.example.App"), "store_app:apple/com.example.App")
        self.assertEqual(cid("container", "anything"), "")
        self.assertEqual(cid("domain", ""), "")

    def test_links_carry_confidence_and_never_claim_more_than_known(self):
        def linking(ctx):
            res = ctx.resource("x:1", "app", "Example app", links=[
                {"rel": "uses", "to": "x:2"},
                {"rel": "uses", "to": "x:3", "confidence": "inferred", "evidence": "config/example.toml"},
                {"rel": "uses", "to": "x:4", "confidence": "certain"},
                {"rel": "", "to": "x:5"}])
            res["observed_at"] = "2031-04-01T00:00:00+00:00"
            return [res]
        collect.READERS["linking_example"] = linking
        try:
            obs = observe([{"id": "l", "provider": "linking_example"}])
        finally:
            del collect.READERS["linking_example"]
        (res,) = obs["resources"]
        self.assertEqual(res["links"], [
            {"rel": "uses", "to": "x:2", "confidence": "observed"},
            {"rel": "uses", "to": "x:3", "confidence": "inferred", "evidence": "config/example.toml"},
            {"rel": "uses", "to": "x:4", "confidence": "inferred"}])
        self.assertEqual(res["observed_at"], "2031-04-01T00:00:00Z")

    def test_proxies_come_only_from_the_given_env(self):
        self.assertEqual(collect.proxies_from_env({"HTTPS_PROXY": "http://p.example:1", "NO_PROXY": "x"}),
                         {"https": "http://p.example:1"})
        self.assertEqual(collect.proxies_from_env({}), {})


def zone(zid, name, account, plan="Free Example"):
    return {"id": zid, "name": name, "status": "active", "plan": {"name": plan},
            "account": {"id": account, "name": "synthetic"}}


ACCT_A, ACCT_B = "aaaa0000aaaa0000aaaa0000aaaa1111", "bbbb0000bbbb0000bbbb0000bbbb2222"
CF_PAGE_1 = R(0, json.dumps({"success": True, "result": [zone("z1", "alpha.example", ACCT_A),
                                                          zone("z2", "beta.example", ACCT_A)],
                             "result_info": {"page": 1, "total_pages": 2}}), "")
CF_PAGE_2 = R(0, json.dumps({"success": True, "result": [zone("z3", "gamma.example", ACCT_B, "Pro Example")],
                             "result_info": {"page": 2, "total_pages": 2}}), "")
CF_ROW = {"id": "dns-example", "provider": "cloudflare", "search_console_source": "search-example",
          "command": ["dns-cli", "api", "GET", "/zones?per_page=2&page={page}"],
          "account_labels": {"1111": "Example DNS account"}}
SC_LIST = R(0, json.dumps({"siteEntry": [
    {"siteUrl": "sc-domain:alpha.example", "permissionLevel": "siteOwner"},
    {"siteUrl": "https://www.beta.example/", "permissionLevel": "siteFullUser"},
    {"siteUrl": "https://delta.example/", "permissionLevel": "siteUnverifiedUser"}]}), "")
SC_ROW = {"id": "search-example", "provider": "search_console", "label": "Example search account",
          "command": ["search-cli", "sites", "list", "--json"]}


class CloudflareAndSearchTests(unittest.TestCase):
    def runner(self, search=SC_LIST):
        return FakeRunner([(["dns-cli", "page=1"], CF_PAGE_1), (["dns-cli", "page=2"], CF_PAGE_2),
                           (["search-cli"], search)])

    def test_zones_from_every_page_under_their_accounts(self):
        obs = observe([CF_ROW, SC_ROW], self.runner())
        domains = {d["label"]: d for d in by_kind(obs, "domain")}
        self.assertEqual(sorted(domains), ["alpha.example", "beta.example", "gamma.example"])
        accounts = {a["label"]: a for a in by_kind(obs, "account") if a["source"] == "dns-example"}
        self.assertEqual(sorted(accounts), ["Cloudflare account …2222", "Example DNS account"])
        self.assertEqual(accounts["Example DNS account"]["facts"]["resources"]["value"], 2)
        self.assertEqual(domains["gamma.example"]["facts"]["plan"]["value"], "Pro Example")
        self.assertEqual(domains["alpha.example"]["parent"], accounts["Example DNS account"]["id"])
        self.assertIsNone(domains["alpha.example"]["facts"]["renews_on"]["value"])
        self.assertNotIn(ACCT_A, json.dumps(obs))
        # a zone is addressed by its name: the canonical id other readers emit for the same domain
        self.assertEqual(domains["alpha.example"]["id"], "domain:alpha.example")

    def test_search_property_by_exact_host_only(self):
        obs = observe([CF_ROW, SC_ROW], self.runner())
        domains = {d["label"]: d for d in by_kind(obs, "domain")}
        sites = {s["label"]: s for s in by_kind(obs, "site")}
        alpha = domains["alpha.example"]
        self.assertIs(alpha["facts"]["has_search_property"]["value"], True)
        self.assertEqual(alpha["links"], [{"rel": "search_property", "to": sites["sc-domain:alpha.example"]["id"],
                                           "confidence": "observed"}])
        # www.beta.example is not beta.example: no match by guessing
        self.assertIs(domains["beta.example"]["facts"]["has_search_property"]["value"], False)
        self.assertIs(domains["gamma.example"]["facts"]["has_search_property"]["value"], False)

    def test_search_source_that_failed_makes_the_fact_unknown(self):
        failed = R(1, "", "error: not logged in, run the login command")
        obs = observe([CF_ROW, SC_ROW], self.runner(search=failed))
        self.assertEqual(source(obs, "search-example")["state"], "needs_sign_in")
        for domain in by_kind(obs, "domain"):
            fact = domain["facts"]["has_search_property"]
            self.assertEqual((fact["state"], fact["value"]), ("needs_sign_in", None))

    def test_skipped_search_source_leaves_the_fact_unreported(self):
        obs = observe([CF_ROW, dict(SC_ROW, enabled=False)], self.runner())
        for domain in by_kind(obs, "domain"):
            self.assertNotIn("has_search_property", domain["facts"])

    def test_refused_token_is_needs_sign_in(self):
        refused = R(0, json.dumps({"success": False, "errors": [{"code": 10000, "message": "Authentication error"}]}), "")
        obs = observe([CF_ROW], FakeRunner([(["dns-cli"], refused)]))
        self.assertEqual(source(obs, "dns-example")["state"], "needs_sign_in")

    def test_page_cap(self):
        obs = observe([dict(CF_ROW, max_pages=1, search_console_source="")], self.runner())
        self.assertEqual(len(by_kind(obs, "domain")), 2)
        self.assertEqual(source(obs, "dns-example")["detail"], "Only the first 1 pages of zones were read; more exist.")

    def test_sites_and_their_account(self):
        obs = observe([SC_ROW], self.runner())
        sites = {s["label"]: s for s in by_kind(obs, "site")}
        self.assertIs(sites["sc-domain:alpha.example"]["facts"]["verified"]["value"], True)
        self.assertIs(sites["https://delta.example/"]["facts"]["verified"]["value"], False)
        self.assertEqual(sites["https://delta.example/"]["facts"]["last_data_day"]["state"], "not_supported")
        (owner,) = by_kind(obs, "account")
        self.assertEqual(owner["label"], "Example search account")
        self.assertEqual(owner["facts"]["resources"]["value"], 3)
        self.assertTrue(all(s["parent"] == owner["id"] for s in sites.values()))


TOKEN = "tok-SYNTHETIC-0123456789-abcdef"
BAL_ROW = {"id": "credit-example", "provider": "balance_http", "vendor": "example_models",
           "label": "Example prepaid credit", "url": "https://api.example/v1/balance",
           "token_env": "EXAMPLE_API_KEY", "balance_path": "balance_infos[0].total_balance",
           "unit_path": "balance_infos[0].currency",
           "env_unset": ["HTTPS_PROXY", "https_proxy"]}
BAL_ENV = {"EXAMPLE_API_KEY": TOKEN, "HTTPS_PROXY": "http://proxy.example:3128", "PATH": "/usr/bin"}


class FakeHttp:
    def __init__(self, status=200, doc=None, error=None):
        self.status, self.doc, self.error, self.calls = status, doc, error, []

    def __call__(self, url, headers, timeout, env=None):
        self.calls.append({"url": url, "headers": dict(headers), "timeout": timeout, "env": dict(env or {})})
        if self.error:
            raise self.error
        return self.status, json.dumps(self.doc or {}).encode()


BAL_DOC = {"is_available": True, "balance_infos": [{"currency": "USD", "total_balance": "12.50"}]}


class BalanceHttpTests(unittest.TestCase):
    def test_balance_is_read_and_the_token_is_never_written(self):
        http = FakeHttp(doc=BAL_DOC)
        obs = observe([BAL_ROW], http=http, env=BAL_ENV)
        (plan,) = by_kind(obs, "plan")
        self.assertEqual(plan["facts"]["balance"]["value"], 12.5)
        self.assertEqual(plan["facts"]["balance"]["unit"], "usd")
        self.assertEqual(plan["provider"], "example_models")
        self.assertEqual(plan["facts"]["ends_on"]["state"], "not_supported")
        self.assertEqual(http.calls[0]["headers"]["Authorization"], f"Bearer {TOKEN}")
        self.assertNotIn(TOKEN, json.dumps(obs))

    def test_proxy_variables_are_dropped_for_this_source_only(self):
        http = FakeHttp(doc=BAL_DOC)
        observe([BAL_ROW], http=http, env=BAL_ENV)
        observe([dict(BAL_ROW, env_unset=[])], http=http, env=BAL_ENV)
        self.assertNotIn("HTTPS_PROXY", http.calls[0]["env"])
        self.assertIn("HTTPS_PROXY", http.calls[1]["env"])

    def test_missing_token_is_needs_sign_in(self):
        http = FakeHttp(doc=BAL_DOC)
        obs = observe([BAL_ROW], http=http, env={"PATH": "/usr/bin"})
        self.assertEqual(source(obs, "credit-example")["state"], "needs_sign_in")
        self.assertIn("EXAMPLE_API_KEY", source(obs, "credit-example")["detail"])
        self.assertEqual(http.calls, [])

    def test_refused_token_is_needs_sign_in(self):
        obs = observe([BAL_ROW], http=FakeHttp(status=401), env=BAL_ENV)
        self.assertEqual(source(obs, "credit-example")["state"], "needs_sign_in")

    def test_an_error_that_quotes_the_token_never_reaches_the_file(self):
        letters_only = "tokSYNTHETICletters"  # no digit, so id masking alone would not hide it
        http = FakeHttp(error=ValueError(f"Invalid header value b'Bearer {letters_only}'"))
        obs = observe([BAL_ROW], http=http, env=dict(BAL_ENV, EXAMPLE_API_KEY=letters_only))
        self.assertEqual(source(obs, "credit-example")["state"], "failed")
        self.assertNotIn("SYNTHETIC", json.dumps(obs))

    def test_token_command(self):
        runner = FakeRunner([(["vault-cli", "read"], R(0, TOKEN + "\n", ""))])
        row = dict(BAL_ROW, token_env=None, token_command=["vault-cli", "read", "example"])
        http = FakeHttp(doc=BAL_DOC)
        obs = observe([row], runner=runner, http=http, env=BAL_ENV)
        self.assertEqual(by_kind(obs, "plan")[0]["facts"]["balance"]["value"], 12.5)
        self.assertNotIn(TOKEN, json.dumps(obs))
        failing = FakeRunner([(["vault-cli"], R(1, "", f"cannot read {TOKEN}"))])
        obs = observe([row], runner=failing, http=http, env=BAL_ENV)
        self.assertEqual(source(obs, "credit-example")["state"], "needs_sign_in")
        self.assertNotIn(TOKEN, json.dumps(obs))

    def test_null_balance_is_not_known_never_zero(self):
        doc = {"data": {"limit_remaining": None, "usage": 0}}
        row = dict(BAL_ROW, balance_path="data.limit_remaining", unit_path=None, unit="usd")
        plan = by_kind(observe([row], http=FakeHttp(doc=doc), env=BAL_ENV), "plan")[0]
        self.assertEqual((plan["facts"]["balance"]["state"], plan["facts"]["balance"]["value"]),
                         ("not_supported", None))

    def test_plain_http_is_refused(self):
        obs = observe([dict(BAL_ROW, url="http://api.example/v1/balance")], http=FakeHttp(doc=BAL_DOC), env=BAL_ENV)
        self.assertEqual(source(obs, "credit-example")["state"], "failed")


def seat_rows(start):
    rows = []
    for hour, account, used in ((0, "seat-a", 40), (1, "seat-b", 70), (2, "seat-a", 95)):
        rows.append({"provider": "example_ai", "windowKind": "weekly", "accountKey": account,
                     "usedPercent": used, "sampledAt": (start + timedelta(hours=hour)).strftime("%Y-%m-%dT%H:%M:%SZ"),
                     "resetsAt": (start + timedelta(days=3)).strftime("%Y-%m-%dT%H:%M:%S+00:00")})
    rows.append({"provider": "other_ai", "windowKind": "weekly", "accountKey": "seat-c", "usedPercent": 1,
                 "sampledAt": start.strftime("%Y-%m-%dT%H:%M:%SZ")})
    return rows


class JsonFileTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="inventory-json-file-test-")
        self.addCleanup(self.tmp.cleanup)
        self.dir = Path(self.tmp.name)

    def seats(self, start, max_age=None):
        path = self.dir / "usage.jsonl"
        path.write_text("\n".join(json.dumps(r) for r in seat_rows(start)) + "\nnot json\n")
        spec = {"label": "Example seat, weekly", "vendor": "example_ai",
                "match": {"provider": "example_ai", "windowKind": "weekly"}, "group_by": "accountKey",
                "observed_at_path": "sampledAt",
                "facts": {"window_used_pct": "usedPercent", "resets_at": "resetsAt", "Bad-Name": "usedPercent"}}
        if max_age:
            spec["max_age_hours"] = max_age
        return observe([{"id": "seats-example", "provider": "json_file", "path": str(path), "plans": [spec]}])

    def test_latest_entry_per_group(self):
        obs = self.seats(CLOCK - timedelta(hours=3))
        plans = sorted(by_kind(obs, "plan"), key=lambda p: p["facts"]["window_used_pct"]["value"])
        self.assertEqual([p["facts"]["window_used_pct"]["value"] for p in plans], [70, 95])
        self.assertEqual(plans[1]["facts"]["window_used_pct"]["unit"], "pct")
        self.assertEqual(plans[1]["facts"]["window_used_pct"]["observed_at"],
                         (CLOCK - timedelta(hours=1)).strftime("%Y-%m-%dT%H:%M:%SZ"))
        self.assertEqual(plans[1]["facts"]["resets_at"]["value"],
                         (CLOCK + timedelta(days=3, hours=-3)).strftime("%Y-%m-%dT%H:%M:%SZ"))
        self.assertTrue(all(p["provider"] == "example_ai" for p in plans))
        self.assertEqual(len({p["label"] for p in plans}), 2)  # two groups get told apart
        self.assertTrue(all(p["label"].startswith("Example seat, weekly #") for p in plans))
        self.assertNotIn("seat-a", json.dumps(obs))
        self.assertIn("2 facts dropped", source(obs, "seats-example")["detail"])  # one bad name per plan
        self.assertTrue(all("Bad-Name" not in p["facts"] for p in plans))

    def test_old_samples_are_not_shown_as_current(self):
        obs = self.seats(CLOCK - timedelta(days=11), max_age=48)
        for plan in by_kind(obs, "plan"):
            fact = plan["facts"]["window_used_pct"]
            self.assertEqual((fact["state"], fact["value"]), ("failed", None))
            self.assertIn("limit 48 h", fact["detail"])

    def test_object_path_and_missing_file(self):
        path = self.dir / "projection.json"
        path.write_text(json.dumps({"providers": {"example_ai": {"healthy": 4, "total": 5,
                                                                 "imported_at": "2031-04-05T01:00:00Z"}}}))
        spec = {"label": "Example seats", "at": "providers.example_ai", "observed_at_path": "imported_at",
                "facts": {"accounts_healthy": {"path": "healthy", "unit": "count"}, "accounts_total": "total",
                          "absent": "nope"}}
        rows = [{"id": "proj", "provider": "json_file", "path": str(path), "plans": [spec]},
                {"id": "gone", "provider": "json_file", "path": str(self.dir / "missing.json"), "plans": [spec]}]
        obs = observe(rows)
        (plan,) = by_kind(obs, "plan")
        self.assertEqual(plan["facts"]["accounts_healthy"]["value"], 4)
        self.assertEqual(plan["facts"]["accounts_total"]["value"], 5)
        self.assertEqual(plan["facts"]["absent"]["state"], "not_supported")
        self.assertEqual(source(obs, "gone")["state"], "failed")
        self.assertEqual(source(obs, "gone")["detail"], "The file does not exist.")


GRAPH = {
    "observed_at": "2031-03-01T00:00:00Z",
    "nodes": [
        {"id": "product:example_app", "kind": "product", "label": "example app", "provider": ""},
        {"id": "repo:git.example/owner/Example-App", "kind": "repo", "label": "owner/Example-App", "provider": "git.example"},
        {"id": "ci:workflow-1", "kind": "ci", "label": "build workflow", "provider": "ci"},
        {"id": "container:host-a/web", "kind": "container", "label": "web (compose example)", "provider": "docker"},
        {"id": "host:host-a", "kind": "host", "label": "host a", "provider": "lan"},
        {"id": "service:host/vps", "kind": "service", "label": "vps as a service", "provider": "self-hosted"},
        {"id": "domain:www.alpha.example", "kind": "domain", "label": "www.alpha.example", "provider": ""},
        {"id": "zone:Alpha.Example", "kind": "zone", "label": "alpha.example (registrar note)", "provider": "dns"},
        {"id": "alias:help@example.org", "kind": "alias", "label": "help@example.org", "provider": "mail"},
        {"id": "mailbox:owner@example.com", "kind": "mailbox", "label": "owner@example.com", "provider": "mail"},
        {"id": "store_app:com.example.app", "kind": "store_app", "label": "com.example.app", "provider": "apple"},
        {"id": "gizmo:1", "kind": "gizmo", "label": "left out on purpose", "provider": ""},
        {"id": "host:second-name", "kind": "host", "label": "Example server (old name)", "provider": "lan"},
    ],
    "edges": [
        {"from": "product:example_app", "rel": "code_in", "to": "repo:git.example/owner/Example-App",
         "confidence": "observed", "evidence": "example/README.md"},
        {"from": "product:example_app", "rel": "deployed_on", "to": "container:host-a/web", "confidence": "declared",
         "evidence": "a written deployment record"},
        {"from": "product:example_app", "rel": "sold_through", "to": "store_app:com.example.app", "confidence": "observed"},
        {"from": "product:example_app", "rel": "calls", "to": "service:host/vps", "confidence": "observed"},
        {"from": "container:host-a/web", "rel": "runs_on", "to": "host:host-a", "confidence": "observed"},
        {"from": "container:host-a/web", "rel": "mystery_rel", "to": "host:host-a", "confidence": "observed"},
        {"from": "repo:git.example/owner/Example-App", "rel": "built_by", "to": "ci:workflow-1", "confidence": "observed"},
        {"from": "domain:www.alpha.example", "rel": "points_to", "to": "container:host-a/web", "confidence": "inferred",
         "evidence": "name match"},
        {"from": "alias:help@example.org", "rel": "forwards_to", "to": "mailbox:owner@example.com", "confidence": "inferred"},
        {"from": "service:host/vps", "rel": "uses", "to": "container:host-a/web"},
        {"from": "zone:Alpha.Example", "rel": "owned_by", "to": "gizmo:1"},
        {"from": "host:host-a", "rel": "uses", "to": "host:host-a"},
    ],
}
GRAPH_MAPS = {
    "kind_map": {"product": "product", "repo": "repo", "container": "container", "host": "host", "service": "service",
                 "domain": "hostname", "zone": "domain", "alias": "alias", "mailbox": "mailbox", "store_app": "store_app",
                 "gizmo": None},
    "relation_map": {"code_in": "code_in", "deployed_on": "deployed_on", "sold_through": "sold_through", "calls": "calls",
                     "runs_on": "runs_on", "built_by": "built_by", "points_to": "points_to", "forwards_to": "forwards_to",
                     "uses": "uses", "owned_by": "owned_by"},
}


class EdgesFileTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="inventory-edges-file-test-")
        self.addCleanup(self.tmp.cleanup)
        self.dir = Path(self.tmp.name)
        self.path = self.dir / "graph.json"
        self.path.write_text(json.dumps(GRAPH))
        ssh_id = "ssh:host:" + collect.short_hash("vps.example")
        self.row = dict(GRAPH_MAPS, id="graph-example", provider="edges_file", path=str(self.path),
                        same_as={"service:host/vps": ssh_id}, report_path=str(self.dir / "report.json"))

    def observe(self):
        runner = FakeRunner([(["vps.example"], R(0, LINUX_STATUS, "")), (["dns-cli", "page=1"], CF_PAGE_1),
                             (["dns-cli", "page=2"], CF_PAGE_2)])
        # the graph row comes FIRST: an observed resource still wins over its stand-in
        return observe([self.row, SSH_ROW, dict(CF_ROW, search_console_source="")], runner)

    def test_nodes_become_resources_with_canonical_ids_and_typed_links(self):
        obs = self.observe()
        res = {r["id"]: r for r in obs["resources"]}
        product = res["product:example_app"]
        self.assertEqual(product["kind"], "product")
        links = {(l["rel"], l["to"]): l for l in product["links"]}
        container = next(r for r in obs["resources"] if r["kind"] == "container")
        self.assertEqual(container["id"], "edges_file:container:" + collect.short_hash("container:host-a/web"))
        self.assertEqual(links[("code_in", "repo:git.example/owner/example-app")]["evidence"], "example/README.md")
        self.assertEqual(links[("deployed_on", container["id"])]["confidence"], "declared")
        self.assertIn(("sold_through", "store_app:apple/com.example.app"), links)
        self.assertEqual(res["hostname:www.alpha.example"]["links"][0]["confidence"], "inferred")
        self.assertEqual(res["alias:help@example.org"]["links"][0]["to"], "mailbox:owner@example.com")
        self.assertEqual(container["links"], [{"rel": "runs_on", "to": "host:host-a", "confidence": "observed",
                                               "observed_at": "2031-03-01T00:00:00Z"}])
        self.assertEqual(product["observed_at"], "2031-03-01T00:00:00Z")  # the file's own date
        self.assertEqual(source(obs, "graph-example")["state"], "ok")

    def test_same_as_and_canonical_ids_merge_with_other_readers(self):
        obs = self.observe()
        hosts = [r for r in obs["resources"] if r["id"] == "ssh:host:" + collect.short_hash("vps.example")]
        self.assertEqual(len(hosts), 1)
        (host,) = hosts
        self.assertEqual((host["kind"], host["label"], host["source"]), ("host", "Example server", "vps-example"))
        self.assertEqual([l["rel"] for l in host["links"]], ["uses"])  # the stand-in's link moved onto it
        self.assertIn(("calls", host["id"]), {(l["rel"], l["to"]) for l in
                                             next(r for r in obs["resources"] if r["id"] == "product:example_app")["links"]})
        domains = [r for r in obs["resources"] if r["id"] == "domain:alpha.example"]
        self.assertEqual(len(domains), 1)
        self.assertEqual((domains[0]["label"], domains[0]["source"]), ("alpha.example", "dns-example"))
        # the stand-in for the server and the zone both merged into what this run read itself
        self.assertIn("2 already reported by another source, merged", source(obs, "graph-example")["detail"])

    def test_nothing_is_dropped_silently(self):
        obs = self.observe()
        detail = source(obs, "graph-example")["detail"]
        self.assertTrue(detail.startswith("Kept 11 of 13 nodes and 8 of 12 links; "), detail)
        self.assertIn("kinds not in the map: ci 1", detail)
        self.assertIn("relations not in the map: mystery_rel 1", detail)
        self.assertIn("nodes left out by the map: 1", detail)
        self.assertIn("links missing an end: 2", detail)  # built_by -> ci, owned_by -> gizmo
        self.assertIn("links to themselves: 1", detail)
        self.assertIn("same_as matches: 1", detail)
        self.assertIn("likely duplicates: 1", detail)
        self.assertTrue(detail.endswith(".") and len(detail) <= 300, detail)
        report = json.loads((self.dir / "report.json").read_text())
        self.assertEqual(stat.S_IMODE(os.stat(self.dir / "report.json").st_mode), 0o600)
        self.assertEqual(report["kinds_not_in_map"], {"ci": 1})
        self.assertEqual(report["relations_not_in_map"], {"mystery_rel": 1})
        self.assertEqual([d["label"] for d in report["likely_duplicates"]], ["Example server (old name)"])

    def test_same_input_same_bytes(self):
        self.assertEqual(same_bytes(self.observe()), same_bytes(self.observe()))

    def test_a_stand_in_never_replaces_the_node_it_stands_for(self):
        other = self.dir / "other.json"
        other.write_text(json.dumps({"nodes": [{"id": "host:host-b", "kind": "host", "label": "host b", "provider": "lan"}],
                                     "edges": []}))
        graph = {"nodes": [{"id": "service:host/b", "kind": "service", "label": "b as a service", "provider": ""},
                           {"id": "product:example_app", "kind": "product", "label": "example app", "provider": ""}],
                 "edges": [{"from": "product:example_app", "rel": "calls", "to": "service:host/b"}]}
        self.path.write_text(json.dumps(graph))
        rows = [dict(self.row, same_as={"service:host/b": "host:host-b"}, report_path=None),  # listed first
                dict(GRAPH_MAPS, id="other-graph", provider="edges_file", path=str(other))]
        res = {r["id"]: r for r in observe(rows)["resources"]}
        self.assertEqual((res["host:host-b"]["kind"], res["host:host-b"]["label"]), ("host", "host b"))
        self.assertEqual(res["product:example_app"]["links"][0]["to"], "host:host-b")

    def test_same_as_targets_in_a_files_own_words(self):
        same_as_id = sys.modules["compound_inventory_readers.edges_file"].same_as_id
        self.assertEqual(same_as_id("container:host-a/web", "scout"), "scout:container:" + collect.short_hash("container:host-a/web"))
        self.assertEqual(same_as_id("host:Host A", "scout"), "host:host-a")
        self.assertEqual(same_as_id("ssh:host:0123456789ab", "scout"), "ssh:host:0123456789ab")
        self.assertEqual(same_as_id(None, "scout"), "")

    def test_missing_or_misshapen_file(self):
        self.path.write_text(json.dumps({"nodes": {}}))
        obs = observe([self.row])
        self.assertEqual(source(obs, "graph-example")["detail"],
                         "The nodes-and-links file has no list of nodes and list of links.")
        obs = observe([dict(self.row, path=str(self.dir / "absent.json"))])
        self.assertEqual(source(obs, "graph-example")["detail"], "The nodes-and-links file does not exist.")


SSH_ID = "ssh:host:" + collect.short_hash("vps.example")
DECLARED = {
    "schema_version": "compound.system_inventory_declared.v0.1",
    "nodes": [
        {"id": SSH_ID, "kind": "host", "label": "a person's name for the server", "by": "operator", "on": "2031-03-02",
         "evidence": "hosting invoice"},
        {"id": "example:identity:deploy-key", "kind": "identity", "label": "deploy key (name only)", "by": "agent",
         "on": "2031-03-02", "evidence": "ssh config IdentityFile line"},
        {"id": "hostname:WWW.Beta.Example", "kind": "hostname", "label": "www.beta.example", "by": "operator",
         "on": "2031-03-02", "evidence": "registrar page"},
        {"id": "example:identity:no-evidence", "kind": "identity", "label": "unsupported", "by": "agent", "on": "2031-03-02"},
    ],
    "links": [
        {"from": SSH_ID, "rel": "reached_through", "to": "example:identity:deploy-key", "by": "agent", "on": "2031-03-02",
         "evidence": "ssh config Host block"},
        {"from": "hostname:WWW.Beta.Example", "rel": "points_to", "to": SSH_ID, "by": "operator", "on": "2031-03-02",
         "evidence": "dns record list"},
        {"from": "example:nobody", "rel": "uses", "to": SSH_ID, "by": "agent", "on": "2031-03-02", "evidence": "x"},
    ],
    "facts": [
        {"resource": SSH_ID, "name": "status", "value": "down", "by": "operator", "on": "2031-03-02", "evidence": "a note"},
        {"resource": SSH_ID, "name": "uptime_days", "value": 46, "unit": "days", "by": "operator", "on": "2031-03-02",
         "evidence": "a note"},
        {"resource": SSH_ID, "name": "off_host_backup", "value": True, "unit": "bool", "by": "operator",
         "on": "2031-03-02", "evidence": "backup provider page"},
        {"resource": "example:identity:deploy-key", "name": "second_factor_on_file", "value": False, "unit": "bool",
         "by": "agent", "on": "2031-03-02"},
    ],
}


class DeclaredTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="inventory-declared-test-")
        self.addCleanup(self.tmp.cleanup)
        self.path = Path(self.tmp.name) / "declared.json"
        self.path.write_text(json.dumps(DECLARED))
        self.row = {"id": "declared-example", "provider": "declared", "path": str(self.path)}

    def observe(self):
        # the declared row comes FIRST: what this run observed still wins
        return observe([self.row, SSH_ROW], FakeRunner([(["vps.example"], R(0, LINUX_STATUS, ""))]))

    def test_declared_never_overwrites_observed_and_says_where_they_disagree(self):
        obs = self.observe()
        host = next(r for r in obs["resources"] if r["id"] == SSH_ID)
        self.assertEqual((host["label"], host["source"]), ("Example server", "vps-example"))
        self.assertEqual(host["facts"]["status"]["value"], "up")
        self.assertEqual(host["facts"]["uptime_days"]["value"], 46)  # agrees: kept as observed
        filled = host["facts"]["off_host_backup"]
        self.assertEqual((filled["value"], filled["observed_at"]), (True, "2031-03-02T00:00:00Z"))
        self.assertIn("declared by operator on 2031-03-02: backup provider page", filled["detail"])
        detail = source(obs, "declared-example")["detail"]
        self.assertIn("1 declared facts disagree with what was observed, observed kept "
                      "(first: status of Example server)", detail)

    def test_declared_links_and_nodes_carry_confidence_declared(self):
        obs = self.observe()
        res = {r["id"]: r for r in obs["resources"]}
        link = res[SSH_ID]["links"][0]
        self.assertEqual((link["rel"], link["to"], link["confidence"], link["evidence"]),
                         ("reached_through", "example:identity:deploy-key", "declared", "ssh config Host block"))
        self.assertEqual(res["hostname:www.beta.example"]["links"][0]["to"], SSH_ID)  # canonical id
        self.assertEqual(res["example:identity:deploy-key"]["observed_at"], "2031-03-02T00:00:00Z")

    def test_an_entry_without_who_when_or_evidence_is_left_out_and_counted(self):
        obs = self.observe()
        ids = {r["id"] for r in obs["resources"]}
        self.assertNotIn("example:identity:no-evidence", ids)
        facts = next(r for r in obs["resources"] if r["id"] == "example:identity:deploy-key")["facts"]
        self.assertNotIn("second_factor_on_file", facts)
        detail = source(obs, "declared-example")["detail"]
        self.assertTrue(detail.startswith("Declared 3 nodes, 2 links and 3 facts; left out: "
                                          "2 without by, on or evidence, 1 links from an id that is not a node of the file"), detail)


FAKE_VALUES = ("FAKE-VALUE-NEVER-IN-OUTPUT-1", "FAKE-VALUE-NEVER-IN-OUTPUT-2", "FAKE-VALUE-NEVER-IN-OUTPUT-3",
               "FAKE-VALUE-NEVER-IN-OUTPUT-4", "FAKE-VALUE-NEVER-IN-OUTPUT-5", "FAKE-VALUE-NEVER-IN-OUTPUT-6", "ABCDE12345")
APP_FILES = {
    ".env": f"API_KEY={FAKE_VALUES[0]}\nexport STRIPE_SECRET_KEY: {FAKE_VALUES[1]}\n# a comment\n",
    "wrangler.toml": ('name = "app-one-api"\nroutes = [{ pattern = "api.app-one.example/*" }]\n'
                      'kv_namespaces = [{ binding = "CACHE", id = "kv-id" }]\n'
                      'r2_buckets = [{ binding = "FILES", bucket_name = "app-one-files" }]\n'
                      f'[vars]\nSECRET_TOKEN = "{FAKE_VALUES[2]}"\n'),
    "docker-compose.yml": ("services:\n  web:\n    image: registry.example/web:1\n    environment:\n"
                           f"      DB_PASSWORD: {FAKE_VALUES[3]}\n      - DATABASE_URL=postgres" f"://user:{FAKE_VALUES[4]}@db/app\n"
                           "    labels:\n      - \"traefik.http.routers.web.rule=Host(`www.app-one.example`)\"\n"
                           "  db:\n    image: postgres:16\nvolumes:\n  data: {}\n"),
    ".github/workflows/deploy.yml": ("name: Deploy site\non: push\njobs:\n  deploy:\n    runs-on: ubuntu-latest\n"
                                     "    env:\n      CLOUDFLARE_API_TOKEN: ${{ secrets.CLOUDFLARE_API_TOKEN }}\n"
                                     f"      HARDCODED: {FAKE_VALUES[5]}\n    steps:\n      - run: npx wrangler deploy\n"),
    "package.json": json.dumps({"dependencies": {"posthog-js": "1.0.0", "stripe": "1.0.0"}}),
    "App/App.xcodeproj/project.pbxproj": (f"PRODUCT_BUNDLE_IDENTIFIER = com.example.appone;\nDEVELOPMENT_TEAM = {FAKE_VALUES[6]};\n"
                                          "PRODUCT_BUNDLE_IDENTIFIER = com.example.appone.widget;\n"),
    "App/App.entitlements": ('<?xml version="1.0" encoding="UTF-8"?>\n<plist version="1.0"><dict>'
                             "<key>com.apple.developer.healthkit</key><true/>"
                             "<key>com.apple.developer.icloud-container-identifiers</key>"
                             "<array><string>iCloud.com.example.appone</string></array></dict></plist>\n"),
    "tests/test_billing.py": "import stripe\n",
}


class RepoScanTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="inventory-repo-scan-test-")
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name) / "products"
        self.app, self.site = self.root / "app-one", self.root / "site-two"
        for name, text in APP_FILES.items():
            (self.app / name).parent.mkdir(parents=True, exist_ok=True)
            (self.app / name).write_text(text)
        (self.site / "node_modules" / "dep" / ".git").mkdir(parents=True)  # skipped folder
        (self.site / "CNAME").write_text("www.site-two.example\n")
        (self.app / ".git").mkdir()
        (self.site / ".git").mkdir()
        products = [{"product_id": "product:app_one", "surfaces": [f"file://{self.app}/App", "${HOME}/elsewhere"]}]
        self.runner = FakeRunner([
            (["app-one", "ls-files"], R(0, "\0".join(sorted(APP_FILES)) + "\0", "")),
            (["app-one", "remote"], R(0, "origin\tgit@example.com:Owner/App-One.git (fetch)\n"
                                         "origin\tgit@example.com:Owner/App-One.git (push)\n", "")),
            (["app-one", "log"], R(0, "2031-03-01T10:00:00+00:00\n", "")),
            (["site-two", "ls-files"], R(0, "CNAME\0", "")), (["site-two", "remote"], R(0, "", "")),
            (["site-two", "log"], R(0, "2031-02-01T00:00:00Z\n", "")),
            (["product-cli"], R(0, json.dumps(products), ""))])
        self.row = {"id": "repos-example", "provider": "repo_scan", "roots": [str(self.root)],
                    "product_command": ["product-cli", "where", "--json"]}

    def observe(self):
        return observe([self.row], self.runner)

    def test_a_fake_secret_value_appears_nowhere_in_the_output(self):
        text = json.dumps(self.observe())
        for value in FAKE_VALUES:
            self.assertNotIn(value, text)
        self.assertNotIn("kv-id", text)

    def test_git_is_only_asked_locally(self):
        self.observe()
        git = [" ".join(c) for c, _, _ in self.runner.calls if c[0] == "git"]
        self.assertEqual(len(git), 6)
        for call in git:
            self.assertRegex(call, r" (ls-files -z|remote -v|log -1 --format=%cI)$")

    def test_repos_products_and_links(self):
        obs = self.observe()
        res = {r["id"]: r for r in obs["resources"]}
        repo, local = res["repo:example.com/owner/app-one"], res["repo:local/products-site-two"]
        self.assertEqual((repo["facts"]["has_remote"]["value"], repo["facts"]["last_commit"]["value"]), (True, "2031-03-01"))
        self.assertEqual(local["facts"]["has_remote"]["value"], False)
        product = res["product:app_one"]
        links = {(l["rel"], l["to"]) for l in product["links"]}
        worker = "repo_scan:deployment:" + collect.short_hash("deployment:cloudflare-worker/app-one-api")
        compose = "repo_scan:deployment:" + collect.short_hash("deployment:compose/web")
        self.assertTrue({("code_in", repo["id"]), ("deployed_on", worker), ("deployed_on", compose),
                         ("sold_through", "store_app:apple/com.example.appone")} <= links, links)
        self.assertIn(("uses", "repo_scan:datastore:" + collect.short_hash("datastore:postgres-container/products-app-one")), links)
        names = {res[to]["label"] for rel, to in links if rel == "uses"}
        self.assertTrue({"Stripe", "PostHog"} <= names, names)
        self.assertNotIn("store_app:apple/com.example.appone.widget", res)
        self.assertEqual({l["to"] for l in res[worker]["links"] if l["rel"] == "serves"}, {"hostname:api.app-one.example"})
        self.assertEqual(len([l for l in res[worker]["links"] if l["rel"] == "uses"]), 2)  # KV and R2
        self.assertEqual(res[worker]["facts"]["live"]["value"], "not_known")
        self.assertEqual({l["to"] for l in res[compose]["links"]}, {"hostname:www.app-one.example"})
        app = res["store_app:apple/com.example.appone"]
        self.assertEqual({res[l["to"]]["label"] for l in app["links"] if l["rel"] == "uses"},
                         {"Apple HealthKit", "CloudKit container iCloud.com.example.appone"})
        self.assertEqual([res[l["to"]]["kind"] for l in repo["links"] if l["rel"] == "built_by"], ["external_service"])
        self.assertEqual(repo["facts"]["secret_names"]["value"], 1)
        self.assertIn("DB_PASSWORD", repo["facts"]["env_names"]["detail"])
        pages = [l for l in local["links"] if l["rel"] == "deployed_on"]  # no product: the repo holds it
        self.assertEqual(len(pages), 1)
        self.assertEqual(res[pages[0]["to"]]["links"][0]["to"], "hostname:www.site-two.example")
        self.assertIn("Read 2 of 2 repositories; 1 belong to a known product", source(obs, "repos-example")["detail"])

    def test_same_input_same_bytes(self):
        self.assertEqual(same_bytes(self.observe()), same_bytes(self.observe()))

    def test_the_persons_product_map_wins(self):
        obs = observe([dict(self.row, product_map={"products/site-two": ["product:site_two"], "products/app-one": "product:other"})],
                      self.runner)
        res = {r["id"]: r for r in obs["resources"]}
        self.assertIn("product:site_two", res)
        self.assertIn("product:other", res)
        self.assertNotIn("product:app_one", res)


class MergeTests(unittest.TestCase):
    def run_reader(self, fn, rows=None):
        collect.READERS["merge_example"] = fn
        try:
            return observe(rows or [{"id": "m", "provider": "merge_example"}])
        finally:
            del collect.READERS["merge_example"]

    def test_strongest_link_wins_and_unknown_kinds_and_relations_are_counted(self):
        def reader(ctx):
            return [ctx.resource("x:1", "service", "one", links=[
                        {"rel": "uses", "to": "x:2", "confidence": "inferred"},
                        {"rel": "uses", "to": "x:2", "confidence": "observed"},
                        {"rel": "invented_rel", "to": "x:2"}]),
                    ctx.resource("x:2", "invented_kind", "two"),
                    ctx.resource("product:p", "service", "wrong kind for a product id"),
                    ctx.resource("x:1", "service", "one again", links=[{"rel": "calls", "to": "x:3"}])]
        obs = self.run_reader(reader)
        (res,) = obs["resources"]
        self.assertEqual([(l["rel"], l["to"], l["confidence"]) for l in res["links"]],
                         [("uses", "x:2", "observed"), ("calls", "x:3", "observed")])
        detail = source(obs, "m")["detail"]
        for words in ("1 already reported by another source, merged",
                      "1 resources dropped: kind not in the inventory kinds file",
                      "1 resources dropped: a product: id needs the kind product, and the other way round",
                      "1 links dropped: relation not in the inventory kinds file"):
            self.assertIn(words, detail)


class PlainFailureTextTests(unittest.TestCase):
    """A source's detail is one plain sentence: never an exception class, a command line or a home path."""

    def test_a_real_timeout_names_the_tool_by_its_purpose(self):
        # the real run door, a real child process that outlives its time limit
        row = dict(CF_ROW, search_console_source="", timeout_s=0.3,
                   command=[sys.executable, "-c", "import time; time.sleep(5)"])
        obs = collect.collect({"sources": [row]}, runner=collect.run, now=now, timeout_s=5, env={})
        detail = source(obs, "dns-example")["detail"]
        self.assertEqual(source(obs, "dns-example")["state"], "failed")
        self.assertEqual(detail, "The DNS listing tool did not answer within 0.3 seconds.")
        for word in ("Timeout", "Expired", "sleep", "-c", os.path.basename(sys.executable)):
            self.assertNotIn(word, detail)

    def test_a_missing_tool_is_a_sentence(self):
        row = dict(SC_ROW, command=["example-tool-that-is-not-installed", "list"])
        obs = collect.collect({"sources": [row]}, runner=collect.run, now=now, timeout_s=5, env={"PATH": "/nonexistent"})
        self.assertEqual(source(obs, "search-example")["detail"],
                         "The search property listing tool is not installed on this computer, or the job cannot find it.")

    def test_tool_words_lose_home_paths_and_exception_classes(self):
        err = ("Traceback (most recent call last):\nValueError: cannot open /home/example/.config/tool/settings.json "
               "or /Users/example/Library/x")
        obs = observe([SC_ROW], FakeRunner([(["search-cli"], R(1, "", err))]))
        detail = source(obs, "search-example")["detail"]
        self.assertTrue(detail.startswith("The search property listing tool could not give the property list"), detail)
        self.assertTrue(detail.endswith("."))
        for word in ("ValueError", "Traceback", "/example/", "/home/", "/Users/"):
            self.assertNotIn(word, detail)
        self.assertIn("~/.config/tool/settings.json", detail)

    def test_every_source_detail_is_one_sentence(self):
        rows = [dict(SSH_ROW, enabled=False), {"id": "u", "provider": "no_such_reader"}, PVE_ROW]
        obs = observe(rows, FakeRunner([(["pve.example"], R(255, "", "pve.example: Permission denied (publickey)."))]))
        for src in obs["sources"]:
            self.assertRegex(src["detail"], r"^[A-Z0-9].*[.]$", src["id"])
            self.assertNotIn("\n", src["detail"])


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
