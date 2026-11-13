"""What every reader shares: the two doors to the outside (`run`, `http_get`), the per-source
`Context`, the reader registry, and small parsing helpers. Readers import from here only;
`collect.py` owns the run over all sources."""

import collections
import hashlib
import json
import math
import os
import re
import subprocess
import urllib.error
import urllib.request
from datetime import datetime, timezone

SOURCE_STATES = ("ok", "failed", "needs_sign_in", "needs_owner", "costs_money", "not_supported", "skipped")
FACT_STATES = ("ok", "needs_sign_in", "needs_owner", "costs_money", "not_supported", "failed")
DETAIL_MAX = 200
PCT = "pct"  # the unit name the inventory kinds file uses for percentages

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


# --- links and canonical ids (observation contract v0.2) -------------------------------------

# How sure a link is: a command or config file shows it now | a person or a written record says
# it | concluded from a name or a match. Weakest last.
LINK_CONFIDENCE = ("observed", "declared", "inferred")
# Kinds whose things are addressed by a name: two readers that see the same thing emit the same id.
CANONICAL_KINDS = ("domain", "hostname", "mailbox", "alias", "repo", "host", "store_app")
_SCP_REMOTE = re.compile(r"^[\w.-]+@([\w.-]+):(.+)$")
_URL_REMOTE = re.compile(r"^[a-z][a-z0-9+.-]*://(?:[^/@]+@)?([\w.-]+)(?::\d+)?/(.+)$", re.I)


def repo_ref(remote):
    """`<host>/<owner>/<name>` (lowercase, no `.git`) from a git remote URL or an already short
    form; "" when it is a local path or cannot be read."""
    text = str(remote or "").strip()
    match = _SCP_REMOTE.match(text) or _URL_REMOTE.match(text)
    if match:
        host, path = match.group(1), match.group(2)
    elif "://" not in text and not text.startswith(("/", ".", "~")) and text.count("/") >= 2:
        host, path = text.split("/", 1)
    else:
        return ""
    path = re.sub(r"\.git/?$", "", path.strip("/"))
    if not host or not path:
        return ""
    return f"{host}/{path}".lower()


def canonical_id(kind, name):
    """The canonical id for a thing addressed by a name, or "" when `kind` has none or `name` is
    empty: `domain:<name>`, `hostname:<fqdn>`, `mailbox:<address>`, `alias:<address>`,
    `repo:<host>/<owner>/<name>` (or `repo:local/<folder>`), `host:<label>`, `store_app:<store>/<app id>`."""
    text = str(name or "").strip()
    if kind in ("domain", "hostname"):
        text = text.lower().rstrip(".")
        text = re.sub(r"^[a-z][a-z0-9+.-]*://", "", text).split("/", 1)[0].split(":", 1)[0]
    elif kind in ("mailbox", "alias"):
        text = text.lower()
        text = text[len("mailto:"):] if text.startswith("mailto:") else text
    elif kind == "repo":
        if text.lower().startswith("local/"):
            text = "local/" + text[len("local/"):].strip("/")
        else:
            text = repo_ref(text)
    elif kind == "host":
        text = re.sub(r"\s+", "-", text.lower())
    elif kind == "store_app":
        store, _, app = text.partition("/")
        text = f"{store.lower()}/{app}" if store and app else ""
    else:
        return ""
    return f"{kind}:{text}" if text else ""


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


# --- shared ssh helpers (pve, ssh_host) -------------------------------------------------------

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


# --- shared parsing ---------------------------------------------------------------------------

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
