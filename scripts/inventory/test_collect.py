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

    def test_search_property_by_exact_host_only(self):
        obs = observe([CF_ROW, SC_ROW], self.runner())
        domains = {d["label"]: d for d in by_kind(obs, "domain")}
        sites = {s["label"]: s for s in by_kind(obs, "site")}
        alpha = domains["alpha.example"]
        self.assertIs(alpha["facts"]["has_search_property"]["value"], True)
        self.assertEqual(alpha["links"], [{"rel": "search_property", "to": sites["sc-domain:alpha.example"]["id"]}])
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
        self.assertIn("capped at 1 pages", source(obs, "dns-example")["detail"])

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
        self.assertEqual(source(obs, "gone")["detail"], "the file does not exist")


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
