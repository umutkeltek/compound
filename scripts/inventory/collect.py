#!/usr/bin/env python3
"""Inventory collector: observe accounts, hosts, sites and paid plans; write one observation file.

This script lives outside the Compound core. It runs free, read-only listing commands with
the user's own signed-in tools, as named in a private sources file, and writes the file
that `compound system inventory refresh --collector-json FILE` imports.

  python3 -I scripts/inventory/collect.py --sources SOURCES.json --out OBSERVATION.json
          [--only SOURCE_ID] [--timeout-s 20] [--verbose]

Rules (see README.md): it never prints, copies or stores a key, token or password; it makes
only free, read-only calls; one failing source never stops the others, it becomes a source
row with a state and a plain detail. Standard library only.

Exit codes: 0 the observation file was written (failed sources are data, not errors),
1 the file could not be written, 2 bad arguments or a bad sources file.
"""

import argparse
import collections
import hashlib
import json
import math
import os
import re
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timezone

OBSERVATION_SCHEMA = "compound.system_inventory_observation.v0.1"
SOURCES_SCHEMA = "compound.system_inventory_sources.v0.1"
COLLECTOR = {"name": "compound-inventory-collect", "version": "0.1.0"}
SOURCE_STATES = ("ok", "failed", "needs_sign_in", "needs_owner", "costs_money", "not_supported", "skipped")
FACT_STATES = ("ok", "needs_sign_in", "needs_owner", "costs_money", "not_supported", "failed")
DETAIL_MAX = 200
PCT = "pct"  # the unit name the inventory kinds file uses for percentages
EXIT_OK, EXIT_WRITE, EXIT_USAGE = 0, 1, 2

Result = collections.namedtuple("Result", "code out err")


class SourceError(Exception):
    """A source could not be read. `state` is a source state, `detail` is plain words."""

    def __init__(self, state, detail):
        super().__init__(detail)
        self.state = state if state in SOURCE_STATES else "failed"
        self.detail = detail


# --- helpers ------------------------------------------------------------------------------

_LONG_TOKEN = re.compile(r"[A-Za-z0-9_\-]{12,}")


def mask_ids(text):
    """Mask every long token that contains a digit (account ids, key ids, ARNs) to its last 4."""
    def repl(match):
        token = match.group(0)
        return "…" + token[-4:] if any(ch.isdigit() for ch in token) else token
    return _LONG_TOKEN.sub(repl, text)


def clip(text):
    """One masked line of at most DETAIL_MAX characters, for a source or fact detail."""
    lines = [line.strip() for line in str(text).splitlines() if line.strip()]
    line = lines[-1] if lines else ""
    line = mask_ids(line)
    return line if len(line) <= DETAIL_MAX else line[: DETAIL_MAX - 1] + "…"


def short_hash(*parts):
    """Stable opaque id part: sha256 of the joined parts, first 12 hex characters."""
    return hashlib.sha256("\x1f".join(str(p) for p in parts).encode()).hexdigest()[:12]


def iso(moment):
    return moment.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def utc_now():
    return datetime.now(timezone.utc)


def parse_time(text):
    """Parse an ISO-8601 time (with Z or an offset); None when it is not one."""
    if not isinstance(text, str) or not text:
        return None
    try:
        moment = datetime.fromisoformat(text.replace("Z", "+00:00"))
    except ValueError:
        return None
    return moment if moment.tzinfo else moment.replace(tzinfo=timezone.utc)


def pct(part, whole):
    try:
        part, whole = float(part), float(whole)
    except (TypeError, ValueError):
        return None
    return round(100.0 * part / whole, 1) if whole > 0 else None


def parse_json(text, what):
    try:
        return json.loads(text)
    except (TypeError, ValueError):
        raise SourceError("failed", f"{what}: the output is not JSON")


def proxies_from_env(env):
    """Proxy settings taken only from `env`, so a source can drop them with `env_unset`."""
    proxies = {}
    for key, value in env.items():
        low = key.lower()
        if value and low.endswith("_proxy") and low != "no_proxy":
            proxies[low[: -len("_proxy")]] = value
    return proxies


# --- the two doors to the outside (tests replace both) -------------------------------------

def run(cmd, timeout, env=None):
    """Run one command: no shell, no stdin, captured output. A non-zero exit is a Result."""
    tool = os.path.basename(cmd[0]) if cmd else "?"
    try:
        done = subprocess.run(cmd, stdin=subprocess.DEVNULL, capture_output=True, text=True,
                              errors="replace", timeout=timeout, env=env)
    except subprocess.TimeoutExpired:
        raise SourceError("failed", f"{tool} timed out after {timeout:g} s")
    except FileNotFoundError:
        raise SourceError("failed", f"{tool} is not installed or not on PATH")
    except OSError as exc:
        raise SourceError("failed", f"{tool} could not start ({type(exc).__name__})")
    return Result(done.returncode, done.stdout, done.stderr)


def http_get(url, headers, timeout, env=None):
    """One HTTPS GET. Returns (status, body bytes). Headers never appear in an error detail."""
    opener = urllib.request.build_opener(
        urllib.request.ProxyHandler(proxies_from_env(os.environ if env is None else env)))
    request = urllib.request.Request(url, headers=headers, method="GET")
    try:
        with opener.open(request, timeout=timeout) as response:
            return response.status, response.read(2_000_000)
    except urllib.error.HTTPError as exc:
        return exc.code, b""
    except urllib.error.URLError as exc:
        raise SourceError("failed", f"request failed ({type(exc.reason).__name__})")
    except TimeoutError:
        raise SourceError("failed", f"request timed out after {timeout:g} s")
    except OSError as exc:
        raise SourceError("failed", f"request failed ({type(exc).__name__})")


# --- per-source context ---------------------------------------------------------------------

class Context:
    """What a reader gets: its sources row, the run and HTTP doors, a clock, and fact helpers."""

    def __init__(self, row, runner, http, now, timeout_s, base_env):
        self.row = row
        self.id = row["id"]
        self.provider = row["provider"]
        self.vendor = row.get("vendor") or row["provider"]
        self.timeout = float(row.get("timeout_s") or timeout_s)
        drop = set(row.get("env_unset") or [])
        self.env = {k: v for k, v in base_env.items() if k not in drop}
        self.now = now
        self.observed_at = iso(now())
        self._runner = runner
        self._http = http
        self.state = "ok"
        self.detail = ""
        self.after = []

    def require(self, key):
        value = self.row.get(key)
        if value in (None, "", []):
            raise SourceError("failed", f"the sources row has no '{key}'")
        return value

    def run(self, cmd):
        return self._runner(list(cmd), self.timeout, self.env)

    def http_get(self, url, headers):
        return self._http(url, headers, self.timeout, self.env)

    def fact(self, value, unit="", detail="", observed_at=None):
        return {"value": value, "unit": unit, "state": "ok", "detail": detail,
                "observed_at": observed_at or self.observed_at}

    def unknown(self, state, detail, unit=""):
        return {"value": None, "unit": unit, "state": state, "detail": detail,
                "observed_at": self.observed_at}

    def partial(self, state, detail):
        """Keep the resources but mark the source (for example identity ok, listing failed)."""
        self.state, self.detail = state, detail

    def resource(self, rid, kind, label, parent="", facts=None, places=None, links=None, provider=None):
        return {"id": rid, "kind": kind, "provider": provider or self.vendor, "label": label,
                "parent": parent, "source": self.id, "places": list(places or []),
                "links": list(links or []), "facts": dict(facts or {})}


READERS = {}


def reader(name):
    """Register a reader by provider name. A reader takes a Context and returns resources."""
    def register(fn):
        READERS[name] = fn
        return fn
    return register


# --- readers: aws ---------------------------------------------------------------------------

_AWS_SIGN_IN = re.compile(r"ExpiredToken|token has expired|InvalidClientTokenId|UnrecognizedClient|"
                          r"Unable to locate credentials|SSO session|sso login|"
                          r"security token included in the request is (invalid|expired)", re.I)
_AWS_DENIED = re.compile(r"AccessDenied|not authorized", re.I)


def aws_error(result, what):
    err = result.err or ""
    if _AWS_SIGN_IN.search(err):
        return SourceError("needs_sign_in", f"{what}: the sign-in is expired or missing; sign in again")
    if _AWS_DENIED.search(err):
        return SourceError("needs_owner", f"{what}: permission denied; the account owner must allow it")
    if "could not be found" in err:
        return SourceError("failed", f"{what}: the profile is not configured")
    return SourceError("failed", f"{what} failed (exit {result.code}): {clip(err)}")


@reader("aws")
def read_aws(ctx):
    """One `account` per profile: identity check, then the free tagging API listing (capped)."""
    profile = ctx.require("profile")
    base = ["aws", "--profile", profile, "--output", "json", "--no-cli-pager"]
    who = ctx.run(base + ["sts", "get-caller-identity"])
    if who.code != 0:
        raise aws_error(who, "identity check")
    account = str(parse_json(who.out, "identity check").get("Account") or "")
    if not account:
        raise SourceError("failed", "identity check returned no account")
    region = ctx.row.get("region") or ""
    if not region:
        got = ctx.run(["aws", "configure", "get", "region", "--profile", profile])
        region = got.out.strip() if got.code == 0 else ""
    region = region or "us-east-1"
    cap = int(ctx.row.get("max_resources") or 2000)
    facts = {
        "identity_ok": ctx.fact(True, "bool"),
        "account_tail": ctx.fact(account[-4:]),
        "region": ctx.fact(region),
        "cost_month_to_date": ctx.unknown("costs_money", "Cost Explorer charges per request; not called", "usd"),
        "credit_left": ctx.unknown("needs_owner", "credits are shown only in the billing console", "usd"),
        "credit_ends": ctx.unknown("needs_owner", "credit end dates are shown only in the billing console", "date"),
    }
    listing = ctx.run(base + ["--region", region, "resourcegroupstaggingapi", "get-resources",
                              "--max-items", str(cap)])
    if listing.code != 0:
        err = aws_error(listing, "resource listing")
        facts["resources"] = ctx.unknown(err.state if err.state in FACT_STATES else "failed", err.detail, "count")
        ctx.partial(err.state, "identity ok; " + err.detail)
    else:
        data = parse_json(listing.out, "resource listing")
        items = data.get("ResourceTagMappingList") or []
        services = collections.Counter(
            (str(item.get("ResourceARN") or "").split(":") + ["", "", "?"])[2] or "?" for item in items)
        top = ", ".join(f"{name} {count}" for name, count in services.most_common(5))
        detail = f"tagged resources in {region}" + (f" ({top})" if top else "")
        if data.get("NextToken"):
            detail += f"; capped at {cap}, more exist"
        detail += "; untagged resources are not listed"
        facts["resources"] = ctx.fact(len(items), "count", detail)
    label = ctx.row.get("label") or f"AWS account …{account[-4:]}"
    return [ctx.resource(f"aws:account:{short_hash(account)}", "account", label,
                         facts=facts, places=ctx.row.get("places"))]


# --- readers over ssh: pve, ssh_host ---------------------------------------------------------

def ssh_cmd(ctx, remote):
    alias = ctx.require("ssh_alias")
    connect = max(1, min(10, int(ctx.timeout)))
    return ["ssh", "-o", "BatchMode=yes", "-o", f"ConnectTimeout={connect}", alias, remote]


def ssh_error(result):
    err = result.err or ""
    if result.code == 255:
        if "Permission denied" in err:
            return SourceError("needs_sign_in", "the ssh key was not accepted")
        if "Host key verification failed" in err or "IDENTIFICATION HAS CHANGED" in err:
            return SourceError("needs_owner", "the host key is not trusted; confirm it by hand once")
        if "Could not resolve" in err:
            return SourceError("failed", "the host name does not resolve")
        if re.search(r"timed out|No route|refused|unreachable", err, re.I):
            return SourceError("failed", "the host did not answer")
        return SourceError("failed", f"ssh failed: {clip(err)}")
    if re.search(r"permission check failed|permission denied", err, re.I):
        return SourceError("needs_owner", f"the remote command was refused: {clip(err)}")
    return SourceError("failed", f"the remote command failed (exit {result.code}): {clip(err)}")


@reader("pve")
def read_pve(ctx):
    """PVE cluster: `pvesh get /cluster/resources` over ssh -> one `host` per node, one `guest` per VM
    or container."""
    result = ctx.run(ssh_cmd(ctx, "pvesh get /cluster/resources --output-format json"))
    if result.code != 0:
        raise ssh_error(result)
    rows = parse_json(result.out, "cluster resources")
    if not isinstance(rows, list):
        raise SourceError("failed", "cluster resources: expected a list")
    key = ctx.row.get("id_key") or ctx.row["ssh_alias"]
    node_labels = ctx.row.get("node_labels") or {}
    nodes = [r for r in rows if r.get("type") == "node"]
    host_ids = {r.get("node"): f"pve:node:{short_hash(key, r.get('node'))}" for r in nodes}
    running = collections.Counter()
    guests, templates = [], 0
    for r in rows:
        if r.get("type") not in ("qemu", "lxc"):
            continue
        if r.get("template"):
            templates += 1
            continue
        status = str(r.get("status") or "unknown")
        if status == "running":
            running[r.get("node")] += 1
        facts = {"status": ctx.fact(status), "guest_type": ctx.fact(r.get("type"))}
        if status == "running":
            facts["cpu_pct"] = ctx.fact(round(100.0 * float(r.get("cpu") or 0), 1), PCT)
            memory = pct(r.get("mem"), r.get("maxmem"))
            seen_by = "as the hypervisor sees it; can include the guest's cache" if r.get("type") == "qemu" else ""
            facts["memory_pct"] = (ctx.fact(memory, PCT, seen_by) if memory is not None
                                   else ctx.unknown("not_supported", "no memory size reported", PCT))
            if r.get("type") == "lxc":
                disk = pct(r.get("disk"), r.get("maxdisk"))
                facts["disk_pct"] = (ctx.fact(disk, PCT) if disk is not None
                                     else ctx.unknown("not_supported", "no disk size reported", PCT))
            else:
                facts["disk_pct"] = ctx.unknown(
                    "not_supported", "the hypervisor does not see disk use inside a virtual machine; read it on the guest", PCT)
        name = str(r.get("name") or f"{r.get('type')} {r.get('vmid')}")
        parent = host_ids.get(r.get("node"), "")
        guests.append(ctx.resource(f"pve:guest:{short_hash(key, r.get('type'), r.get('vmid'))}", "guest",
                                   name, parent=parent, facts=facts, places=[name]))
    storages = collections.defaultdict(list)
    for r in rows:
        if r.get("type") == "storage" and r.get("status") == "available":
            used = pct(r.get("disk"), r.get("maxdisk"))
            if used is not None:
                storages[r.get("node")].append(used)
    hosts = []
    for r in nodes:
        node = r.get("node")
        status = str(r.get("status") or "unknown")
        facts = {"status": ctx.fact(status), "guests_running": ctx.fact(running[node], "count")}
        if status == "online":
            facts["cpu_pct"] = ctx.fact(round(100.0 * float(r.get("cpu") or 0), 1), PCT)
            facts["memory_pct"] = ctx.fact(pct(r.get("mem"), r.get("maxmem")), PCT)
            facts["disk_pct"] = ctx.fact(pct(r.get("disk"), r.get("maxdisk")), PCT, "root disk")
            facts["cpus"] = ctx.fact(r.get("maxcpu"), "count")
            facts["memory_gib"] = ctx.fact(round(float(r.get("maxmem") or 0) / 2 ** 30, 1), "GiB")
            if storages[node]:
                facts["storage_max_pct"] = ctx.fact(max(storages[node]), PCT,
                                                    f"highest of {len(storages[node])} storages")
        else:
            for name in ("cpu_pct", "memory_pct", "disk_pct"):
                facts[name] = ctx.unknown("failed", f"the node is {status}", PCT)
        label = node_labels.get(node) or (ctx.row.get("label") if len(nodes) == 1 else "") or str(node)
        hosts.append(ctx.resource(host_ids[node], "host", label, facts=facts,
                                  places=ctx.row.get("places") if len(nodes) == 1 else None))
    if templates:
        ctx.detail = f"{templates} templates not listed"
    return hosts + guests


REMOTE_STATUS = ("LC_ALL=C; export LC_ALL; uptime; echo @@df; df -P /; echo @@mem; "
                 "cat /proc/meminfo 2>/dev/null; echo @@stat; head -n 1 /proc/stat 2>/dev/null; "
                 "sleep 1; head -n 1 /proc/stat 2>/dev/null; echo @@end")


def split_sections(text):
    sections, name = {"uptime": []}, "uptime"
    for line in text.splitlines():
        if line.startswith("@@"):
            name = line[2:].strip()
            sections[name] = []
        else:
            sections.setdefault(name, []).append(line)
    return sections


def cpu_busy_pct(stat_lines):
    samples = []
    for line in stat_lines:
        parts = line.split()
        if parts and parts[0] == "cpu":
            try:
                samples.append([int(x) for x in parts[1:]])
            except ValueError:
                return None
    if len(samples) < 2:
        return None
    total = sum(samples[1]) - sum(samples[0])
    idle = (samples[1][3] + (samples[1][4] if len(samples[1]) > 4 else 0)) - \
           (samples[0][3] + (samples[0][4] if len(samples[0]) > 4 else 0))
    return round(100.0 * (total - idle) / total, 1) if total > 0 else None


@reader("ssh_host")
def read_ssh_host(ctx):
    """`uptime`, `df -P /`, and memory and CPU where /proc offers a simple read: one `host`."""
    result = ctx.run(ssh_cmd(ctx, REMOTE_STATUS))
    if result.code != 0 and "@@end" not in (result.out or ""):
        raise ssh_error(result)
    sec = split_sections(result.out or "")
    uptime_text = " ".join(sec.get("uptime", []))
    facts = {"status": ctx.fact("up")}
    days = re.search(r"up\s+(\d+)\s+day", uptime_text)
    if re.search(r"\bup\b", uptime_text):
        facts["uptime_days"] = ctx.fact(int(days.group(1)) if days else 0, "days")
    load = re.search(r"load averages?:\s*([0-9]+(?:\.[0-9]+)?)", uptime_text)
    if load:
        facts["load_1m"] = ctx.fact(float(load.group(1)))
    df_rows = [line.split() for line in sec.get("df", []) if line.strip()]
    if len(df_rows) >= 2 and len(df_rows[-1]) >= 5 and df_rows[-1][4].endswith("%"):
        row = df_rows[-1]
        facts["disk_pct"] = ctx.fact(float(row[4].rstrip("%")), PCT, "root file system")
        try:
            facts["disk_gib"] = ctx.fact(round(int(row[1]) * 1024 / 2 ** 30, 1), "GiB")
        except ValueError:
            pass
    else:
        facts["disk_pct"] = ctx.unknown("failed", "df gave no usable line", PCT)
    mem = {}
    for line in sec.get("mem", []):
        match = re.match(r"(\w+):\s+(\d+)\s*kB", line)
        if match:
            mem[match.group(1)] = int(match.group(2))
    if mem.get("MemTotal") and "MemAvailable" in mem:
        facts["memory_pct"] = ctx.fact(pct(mem["MemTotal"] - mem["MemAvailable"], mem["MemTotal"]), PCT,
                                       "used, not counting reclaimable cache")
        facts["memory_gib"] = ctx.fact(round(mem["MemTotal"] / 2 ** 20, 1), "GiB")
    else:
        facts["memory_pct"] = ctx.unknown("not_supported", "the host has no simple memory read (/proc/meminfo)", PCT)
    busy = cpu_busy_pct(sec.get("stat", []))
    facts["cpu_pct"] = (ctx.fact(busy, PCT, "busy over one second") if busy is not None
                        else ctx.unknown("not_supported", "the host has no simple CPU read (/proc/stat)", PCT))
    key = ctx.row.get("id_key") or ctx.row["ssh_alias"]
    label = ctx.row.get("label") or ctx.row["ssh_alias"]
    return [ctx.resource(f"ssh:host:{short_hash(key)}", "host", label, facts=facts, places=ctx.row.get("places"))]


# --- shared parsing for the readers below ----------------------------------------------------

MISSING = object()
_PATH_PART = re.compile(r"([^.\[\]]+)|\[(-?\d+)\]")
DEFAULT_UNITS = {"window_used_pct": PCT, "resets_at": "time", "ends_on": "date", "credit_ends": "date",
                 "identity_ok": "bool", "verified": "bool", "has_search_property": "bool"}


def json_path(doc, path):
    """`a.b[0].c` lookup; MISSING when any step is absent. `[-1]` is the last item."""
    cur = doc
    for name, index in _PATH_PART.findall(str(path)):
        if name:
            if not isinstance(cur, dict) or name not in cur:
                return MISSING
            cur = cur[name]
        else:
            i = int(index)
            if not isinstance(cur, list) or not -len(cur) <= i < len(cur):
                return MISSING
            cur = cur[i]
    return cur


def as_number(value):
    if isinstance(value, bool):
        return None
    if isinstance(value, (int, float)):
        return value if math.isfinite(value) else None
    if isinstance(value, str):
        try:
            number = float(value.strip())
        except ValueError:
            return None
        return number if math.isfinite(number) else None
    return None


def run_json(ctx, cmd, what):
    """Run a listing command and parse its JSON. Only stderr goes into an error detail."""
    result = ctx.run(cmd)
    if result.code != 0:
        err = result.err or ""
        if re.search(r"log ?in|sign ?in|unauthori[sz]ed|not authenticated|credentials|expired|token", err, re.I):
            raise SourceError("needs_sign_in", f"{what}: the tool is not signed in ({clip(err)})")
        raise SourceError("failed", f"{what} failed (exit {result.code})" + (f": {clip(err)}" if err.strip() else ""))
    return parse_json(result.out, what)


# --- readers: cloudflare, search_console ------------------------------------------------------

_CF_AUTH_CODES = {9103, 9106, 9107, 9109, 10000, 10001}


def cloudflare_error(data, what):
    errors = data.get("errors") or [{}]
    first = errors[0] if isinstance(errors[0], dict) else {"message": str(errors[0])}
    code, message = first.get("code"), str(first.get("message") or "no message")
    if code in _CF_AUTH_CODES or re.search(r"auth|token", message, re.I):
        return SourceError("needs_sign_in", f"{what}: the token was refused ({clip(message)})")
    return SourceError("failed", f"{what}: {clip(message)}")


@reader("cloudflare")
def read_cloudflare(ctx):
    """Zones with their plan, through the command the row names -> `domain`s under their `account`."""
    template = [str(part) for part in ctx.require("command")]
    paged = any("{page}" in part for part in template)
    max_pages = int(ctx.row.get("max_pages") or 10)
    zones, page, pages = [], 1, 1
    while page <= min(pages, max_pages):
        data = run_json(ctx, [part.replace("{page}", str(page)) for part in template], "zone list")
        if not isinstance(data, dict):
            raise SourceError("failed", "zone list: expected an object")
        if data.get("success") is False:
            raise cloudflare_error(data, "zone list")
        zones.extend(z for z in (data.get("result") or []) if isinstance(z, dict))
        pages = int((data.get("result_info") or {}).get("total_pages") or 1) if paged else 1
        page += 1
    if pages > max_pages:
        ctx.detail = f"capped at {max_pages} pages of zones"
    labels = ctx.row.get("account_labels") or {}
    by_account = collections.OrderedDict()
    for zone in zones:
        by_account.setdefault(str((zone.get("account") or {}).get("id") or ""), []).append(zone)
    resources, domains = [], []
    for account, account_zones in by_account.items():
        parent = ""
        if account:
            parent = f"cloudflare:account:{short_hash(account)}"
            resources.append(ctx.resource(parent, "account", labels.get(account[-4:]) or f"Cloudflare account …{account[-4:]}",
                                          facts={"identity_ok": ctx.fact(True, "bool"),
                                                 "account_tail": ctx.fact(account[-4:]),
                                                 "resources": ctx.fact(len(account_zones), "count", "zones")}))
        for zone in account_zones:
            name = str(zone.get("name") or "")
            plan = (zone.get("plan") or {}).get("name")
            facts = {
                "plan": ctx.fact(plan) if plan else ctx.unknown("not_supported", "the zone list has no plan"),
                "status": ctx.fact(str(zone.get("status") or "unknown")),
                "renews_on": ctx.unknown("not_supported", "the zone list has no renewal date", "date"),
            }
            domain = ctx.resource(f"cloudflare:zone:{short_hash(zone.get('id') or name)}", "domain", name or "zone",
                                  parent=parent, facts=facts, places=[name] if name else None)
            domain["_host"] = name.lower()
            domains.append(domain)
    search_source = ctx.row.get("search_console_source")
    if search_source:
        ctx.after.append(lambda results: link_search_properties(ctx, results, search_source, domains))
    return resources + domains


def link_search_properties(ctx, results, search_source, domains):
    """Set `has_search_property` by exact host match against the named search source's sites."""
    entry = results.get(search_source)
    if entry is not None and entry["state"] == "skipped":
        return  # not read this run: leave the fact unreported
    if entry is None or entry["state"] != "ok":
        state = entry["state"] if entry is not None and entry["state"] in FACT_STATES else "failed"
        detail = (f"no source '{search_source}' in the sources file" if entry is None
                  else f"the search source '{search_source}' did not answer ({entry['state']})")
        for domain in domains:
            domain["facts"]["has_search_property"] = ctx.unknown(state, detail, "bool")
        return
    sites = {r["_host"]: r["id"] for r in entry["resources"] if r.get("_host")}
    for domain in domains:
        site = sites.get(domain["_host"])
        domain["facts"]["has_search_property"] = ctx.fact(site is not None, "bool", "exact host match")
        if site:
            domain["links"].append({"rel": "search_property", "to": site})


def site_host(url):
    if url.startswith("sc-domain:"):
        return url[len("sc-domain:"):].strip().lower()
    return (urllib.parse.urlsplit(url).hostname or "").lower()


@reader("search_console")
def read_search_console(ctx):
    """Search properties through the command the row names -> `site`s under one `account`."""
    data = run_json(ctx, ctx.require("command"), "property list")
    if isinstance(data, dict):
        entries = data.get("siteEntry") or data.get("sites") or data.get("properties") or []
    elif isinstance(data, list):
        entries = data
    else:
        raise SourceError("failed", "property list: expected an object or a list")
    account = f"{ctx.vendor}:account:{short_hash(ctx.row.get('id_key') or ctx.id)}"
    sites = []
    for entry in entries:
        url = str((entry or {}).get("siteUrl") or (entry or {}).get("site_url") or "") if isinstance(entry, dict) else ""
        if not url:
            continue
        level = str(entry.get("permissionLevel") or "")
        facts = {
            "verified": (ctx.fact(level != "siteUnverifiedUser", "bool", f"permission: {level}") if level
                         else ctx.unknown("not_supported", "the listing has no permission level", "bool")),
            "last_data_day": ctx.unknown("not_supported", "needs a search analytics query; not run", "date"),
        }
        if level:
            facts["permission"] = ctx.fact(level)
        site = ctx.resource(f"{ctx.vendor}:site:{short_hash(url)}", "site", url, parent=account, facts=facts)
        site["_host"] = site_host(url)
        sites.append(site)
    owner = ctx.resource(account, "account", ctx.row.get("label") or ctx.id, places=ctx.row.get("places"),
                         facts={"identity_ok": ctx.fact(True, "bool"),
                                "resources": ctx.fact(len(sites), "count", "search properties")})
    return [owner] + sites


# --- readers: balance_http, json_file ----------------------------------------------------------

def read_token(ctx):
    """The token for one request, from the named environment variable or command. Never stored."""
    env_name, command = ctx.row.get("token_env"), ctx.row.get("token_command")
    if env_name:
        token = (ctx.env.get(env_name) or "").strip()
        if not token:
            raise SourceError("needs_sign_in", f"the environment variable {env_name} is not set")
    elif command:
        result = ctx.run(command)
        lines = (result.out or "").strip().splitlines()
        if result.code != 0 or not lines:
            raise SourceError("needs_sign_in", f"the token command gave no token (exit {result.code})")
        token = lines[0].strip()
    else:
        raise SourceError("failed", "the sources row names neither token_env nor token_command")
    if any(ch.isspace() or ord(ch) < 32 for ch in token):
        raise SourceError("needs_sign_in", "the token has an unexpected shape")
    return token


@reader("balance_http")
def read_balance_http(ctx):
    """A free GET balance endpoint -> one `plan` with `balance` read at `balance_path`."""
    url = str(ctx.require("url"))
    if not url.startswith("https://"):
        raise SourceError("failed", "the url must start with https://")
    balance_path = ctx.require("balance_path")
    token = read_token(ctx)
    scheme = ctx.row.get("auth_scheme", "Bearer")
    headers = {"Accept": "application/json",
               ctx.row.get("auth_header") or "Authorization": f"{scheme} {token}" if scheme else token}
    try:
        status, body = ctx.http_get(url, headers)
    except SourceError:
        raise
    except Exception as exc:  # never let an exception message (which may quote a header) through
        raise SourceError("failed", f"request failed ({type(exc).__name__})")
    finally:
        token = headers = None
    if status in (401, 403):
        raise SourceError("needs_sign_in", f"the endpoint refused the token (HTTP {status})")
    if status != 200:
        raise SourceError("failed", f"the endpoint answered HTTP {status}")
    data = parse_json((body or b"").decode("utf-8", "replace"), "balance")
    unit = str(ctx.row.get("unit") or "")
    if ctx.row.get("unit_path"):
        found = json_path(data, ctx.row["unit_path"])
        if isinstance(found, str) and found.strip():
            unit = found.strip().lower()
    value = json_path(data, balance_path)
    number = as_number(value)
    if value is MISSING or value is None:
        balance = ctx.unknown("not_supported", f"the endpoint gave no value at {balance_path}", unit)
    elif number is None:
        balance = ctx.unknown("failed", f"the value at {balance_path} is not a number", unit)
    else:
        balance = ctx.fact(number, unit)
    ends_path = ctx.row.get("ends_on_path")
    ends = json_path(data, ends_path) if ends_path else MISSING
    if ends is MISSING or ends is None or isinstance(ends, (dict, list)):
        ends_on = ctx.unknown("not_supported", "the endpoint gives no end date", "date")
    else:
        ends_on = ctx.fact(str(ends), "date")
    return [ctx.resource(f"{ctx.vendor}:plan:{short_hash(ctx.row.get('id_key') or ctx.id)}", "plan",
                         ctx.row.get("label") or ctx.id, facts={"balance": balance, "ends_on": ends_on},
                         places=ctx.row.get("places"))]


def read_document(path, fmt):
    try:
        with open(path, encoding="utf-8") as handle:
            text = handle.read()
    except FileNotFoundError:
        raise SourceError("failed", "the file does not exist")
    except OSError as exc:
        raise SourceError("failed", f"the file cannot be read ({type(exc).__name__})")
    if fmt != "jsonl":
        return parse_json(text, "file")
    rows = []
    for line in text.splitlines():
        try:
            rows.append(json.loads(line))
        except ValueError:
            continue
    return rows


def pick_entries(doc, spec):
    """[(group key, entry or None)] for one plan spec: `at`, or `match` (+ `group_by`) over items."""
    if spec.get("at"):
        found = json_path(doc, spec["at"])
        return [("", found if isinstance(found, dict) else None)]
    items = json_path(doc, spec["items_path"]) if spec.get("items_path") else doc
    match = spec.get("match") or {}
    chosen = [it for it in (items if isinstance(items, list) else [])
              if isinstance(it, dict) and all(json_path(it, k) == v for k, v in match.items())]
    if not spec.get("group_by"):
        return [("", chosen[-1] if chosen else None)]
    latest = collections.OrderedDict()
    for item in chosen:  # the file is in time order: the last entry per group wins
        key = json_path(item, spec["group_by"])
        if key is not MISSING and key is not None:
            latest[str(key)] = item
    return list(latest.items()) or [("", None)]


@reader("json_file")
def read_json_file(ctx):
    """Numbers another tool already publishes in a local JSON or JSONL file -> `plan`s."""
    path = os.path.expanduser(str(ctx.require("path")))
    fmt = ctx.row.get("format") or ("jsonl" if path.endswith(".jsonl") else "json")
    doc = read_document(path, fmt)
    resources = []
    for spec in ctx.require("plans"):
        label, vendor = spec.get("label") or ctx.id, spec.get("vendor") or ctx.vendor
        max_age = spec.get("max_age_hours")
        for key, item in pick_entries(doc, spec):
            sampled = None
            if item is not None and spec.get("observed_at_path"):
                sampled = parse_time(json_path(item, spec["observed_at_path"]))
            observed = iso(sampled) if sampled else None
            too_old = ""
            if sampled and max_age:
                age = (ctx.now() - sampled).total_seconds() / 3600
                if age > float(max_age):
                    too_old = f"the newest entry is {age:.0f} h old (limit {float(max_age):g} h)"
            facts = {}
            for name, fspec in (spec.get("facts") or {}).items():
                fpath = fspec if isinstance(fspec, str) else fspec.get("path")
                unit = DEFAULT_UNITS.get(name, "") if isinstance(fspec, str) else \
                    fspec.get("unit", DEFAULT_UNITS.get(name, ""))
                value = json_path(item, fpath) if item is not None else MISSING
                if item is None:
                    facts[name] = ctx.unknown("failed", "no matching entry in the file", unit)
                elif too_old:
                    facts[name] = ctx.unknown("failed", too_old, unit)
                elif value is MISSING or value is None:
                    facts[name] = ctx.unknown("not_supported", f"the entry has no value at {fpath}", unit)
                elif isinstance(value, (dict, list)):
                    facts[name] = ctx.unknown("failed", f"the value at {fpath} is not a single value", unit)
                else:
                    if unit == "time" and parse_time(value):
                        value = iso(parse_time(value))
                    facts[name] = ctx.fact(value, unit, observed_at=observed)
            suffix = f" …{short_hash(key)[-4:]}" if key else ""
            resources.append(ctx.resource(f"{vendor}:plan:{short_hash(ctx.row.get('id_key') or ctx.id, label, key)}",
                                          "plan", label + suffix, facts=facts, provider=vendor,
                                          places=spec.get("places")))
    return resources


# --- the core -------------------------------------------------------------------------------

def normalize_fact(fact, fallback_time):
    """Enforce the contract: a non-ok fact has value null; an ok fact never has value null."""
    if not isinstance(fact, dict):
        fact = {"value": fact}
    state = fact.get("state") or "ok"
    detail = str(fact.get("detail") or "")
    value = fact.get("value")
    if state not in FACT_STATES:
        state, detail = "failed", f"the reader gave an unknown state ({state})"
    if state != "ok":
        value = None
    elif value is None:
        state, detail = "failed", detail or "the reader returned no value"
    elif isinstance(value, float) and not math.isfinite(value):
        state, value, detail = "failed", None, "the reader returned a number that is not finite"
    elif not isinstance(value, (bool, int, float, str)):
        state, value, detail = "failed", None, "the reader returned a value that is not a number, text or yes/no"
    observed = parse_time(fact.get("observed_at"))
    return {"value": value, "unit": str(fact.get("unit") or ""), "state": state,
            "detail": detail[:DETAIL_MAX], "observed_at": iso(observed) if observed else fallback_time}


FACT_NAME = re.compile(r"^[a-z][a-z0-9_]*$")


def normalize_resource(res, fallback_time):
    """Only the contract's fields, in their types. Returns (resource, number of facts dropped
    because their name is not lowercase letters, digits and _)."""
    out = {key: str(res.get(key) or "") for key in ("id", "kind", "provider", "label", "parent", "source")}
    out["label"] = out["label"] or out["id"]
    if out["parent"] == out["id"]:
        out["parent"] = ""
    out["places"] = [str(p) for p in (res.get("places") or []) if str(p or "").strip()]
    out["links"] = [{"rel": str(l.get("rel")), "to": str(l.get("to"))} for l in (res.get("links") or [])
                    if isinstance(l, dict) and str(l.get("rel") or "").strip() and str(l.get("to") or "").strip()]
    facts = res.get("facts") or {}
    out["facts"] = {name: normalize_fact(f, fallback_time) for name, f in facts.items()
                    if isinstance(name, str) and FACT_NAME.match(name)}
    return out, len(facts) - len(out["facts"])


def collect(doc, runner=run, http=http_get, now=utc_now, only=None, timeout_s=20.0, env=None):
    """Run every enabled source and return the observation document."""
    base_env = dict(os.environ if env is None else env)
    run_started = iso(now())
    results, order = {}, []
    for row in doc["sources"]:
        sid = row["id"]
        order.append(sid)
        entry = {"row": row, "state": "ok", "detail": "", "resources": [], "ctx": None,
                 "observed_at": run_started, "duration_ms": 0}
        results[sid] = entry
        if row.get("enabled", True) is False:
            entry["state"], entry["detail"] = "skipped", str(row.get("note") or "disabled in the sources file")
            continue
        if only and sid != only:
            entry["state"], entry["detail"] = "skipped", "not selected by --only"
            continue
        fn = READERS.get(row["provider"])
        if fn is None:
            entry["state"], entry["detail"] = "not_supported", f"no reader for provider '{row['provider']}'"
            continue
        started = time.monotonic()
        try:
            ctx = Context(row, runner, http, now, timeout_s, base_env)
            entry["ctx"], entry["observed_at"] = ctx, ctx.observed_at
            entry["resources"] = list(fn(ctx) or [])
            entry["state"], entry["detail"] = ctx.state, ctx.detail
        except SourceError as exc:
            entry["state"], entry["detail"] = exc.state, exc.detail
        except Exception as exc:  # a reader bug must not stop the other sources
            entry["state"], entry["detail"] = "failed", f"reader error ({type(exc).__name__}): {clip(exc)}"
        entry["duration_ms"] = int((time.monotonic() - started) * 1000)
    for sid in order:
        ctx = results[sid]["ctx"]
        for hook in (ctx.after if ctx else []):
            try:
                hook(results)
            except Exception as exc:
                results[sid]["detail"] = (results[sid]["detail"] + "; " if results[sid]["detail"] else "") + \
                    f"link step error ({type(exc).__name__})"
    seen, resources, sources = {}, [], []
    for sid in order:
        entry = results[sid]
        dupes = dropped = 0
        for res in entry["resources"]:
            if not res.get("id") or res.get("id") in seen:
                dupes += 1
                continue
            seen[res.get("id")] = sid
            clean, bad_names = normalize_resource(res, entry["observed_at"])
            resources.append(clean)
            dropped += bad_names
        detail = entry["detail"]
        if dupes:
            detail = (detail + "; " if detail else "") + f"{dupes} already reported by another source"
        if dropped:
            detail = (detail + "; " if detail else "") + \
                f"{dropped} facts dropped: a fact name must be lowercase letters, digits and _"
        sources.append({"id": sid, "provider": entry["row"]["provider"], "state": entry["state"],
                        "detail": detail[:DETAIL_MAX], "observed_at": entry["observed_at"],
                        "duration_ms": entry["duration_ms"]})
    for res in resources:  # a parent must be another resource in this file
        if res["parent"] and res["parent"] not in seen:
            res["parent"] = ""
    return {"schema_version": OBSERVATION_SCHEMA, "observed_at": run_started, "collector": dict(COLLECTOR),
            "sources": sources, "resources": resources}


def load_sources(path):
    """Read and check the sources file: a `sources` list of rows with a unique id and a provider."""
    with open(path, encoding="utf-8") as handle:
        doc = json.load(handle)
    if not isinstance(doc, dict) or not isinstance(doc.get("sources"), list):
        raise ValueError("expected an object with a 'sources' list")
    schema = doc.get("schema_version")
    if schema not in (None, SOURCES_SCHEMA):
        raise ValueError(f"unknown schema_version {schema!r}; expected {SOURCES_SCHEMA}")
    ids = set()
    for index, row in enumerate(doc["sources"]):
        if not isinstance(row, dict):
            raise ValueError(f"row {index} is not an object")
        sid, provider = row.get("id"), row.get("provider")
        if not isinstance(sid, str) or not sid or not isinstance(provider, str) or not provider:
            raise ValueError(f"row {index} needs a text 'id' and 'provider'")
        if sid in ids:
            raise ValueError(f"source id {sid!r} is used twice")
        ids.add(sid)
    return doc


def write_private(path, doc):
    """Write the file readable by the owner only, through a temporary file and a rename."""
    tmp = f"{path}.tmp-{os.getpid()}"
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as handle:
        json.dump(doc, handle, indent=1, ensure_ascii=False)
        handle.write("\n")
    os.replace(tmp, path)


def summary_line(obs, seconds, out):
    kinds = collections.Counter(r["kind"] for r in obs["resources"])
    states = collections.Counter(s["state"] for s in obs["sources"])
    kind_text = ", ".join(f"{k} {n}" for k, n in sorted(kinds.items())) or "none"
    state_text = ", ".join(f"{k} {n}" for k, n in sorted(states.items())) or "none"
    return (f"collect: {len(obs['resources'])} resources ({kind_text}) from {len(obs['sources'])} sources "
            f"({state_text}) in {seconds:.1f} s -> {out}")


def main(argv=None, runner=run, http=http_get, now=utc_now):
    parser = argparse.ArgumentParser(description="Observe accounts, hosts, sites and paid plans.")
    parser.add_argument("--sources", required=True, help="the private sources file (JSON)")
    parser.add_argument("--out", required=True, help="where to write the observation file")
    parser.add_argument("--only", help="read only this source id; the others are listed as skipped")
    parser.add_argument("--timeout-s", type=float, default=20.0, help="per-command timeout (default 20)")
    parser.add_argument("--verbose", action="store_true", help="one line per source on stderr")
    args = parser.parse_args(argv)
    if args.timeout_s <= 0:
        print("collect: --timeout-s must be above 0", file=sys.stderr)
        return EXIT_USAGE
    try:
        doc = load_sources(os.path.expanduser(args.sources))
    except (OSError, ValueError) as exc:
        print(f"collect: cannot use the sources file: {clip(exc)}", file=sys.stderr)
        return EXIT_USAGE
    if args.only and args.only not in {row["id"] for row in doc["sources"]}:
        print(f"collect: no source with id {args.only!r}", file=sys.stderr)
        return EXIT_USAGE
    started = time.monotonic()
    obs = collect(doc, runner=runner, http=http, now=now, only=args.only, timeout_s=args.timeout_s)
    out = os.path.expanduser(args.out)
    try:
        write_private(out, obs)
    except OSError as exc:
        print(f"collect: cannot write {out}: {type(exc).__name__}", file=sys.stderr)
        return EXIT_WRITE
    if args.verbose:
        for src in obs["sources"]:
            print(f"  {src['id']}: {src['state']} ({src['duration_ms']} ms) {src['detail']}", file=sys.stderr)
    print(summary_line(obs, time.monotonic() - started, out))
    return EXIT_OK


if __name__ == "__main__":
    sys.exit(main())
