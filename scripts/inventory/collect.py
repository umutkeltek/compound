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

The readers live in `readers/`, one module per provider; this file runs them and writes the file.

Exit codes: 0 the observation file was written (failed sources are data, not errors),
1 the file could not be written, 2 bad arguments or a bad sources file.
"""

import argparse
import collections
import importlib.util
import json
import math
import os
import re
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
_PACKAGE = "compound_inventory_readers"


def _load_readers():
    """Load the `readers` package from this folder by path, so `python3 -I` (which leaves this
    folder off `sys.path`) still finds it and no installed module of the same name can stand in."""
    if _PACKAGE not in sys.modules:
        folder = os.path.join(HERE, "readers")
        spec = importlib.util.spec_from_file_location(_PACKAGE, os.path.join(folder, "__init__.py"),
                                                      submodule_search_locations=[folder])
        module = importlib.util.module_from_spec(spec)
        sys.modules[_PACKAGE] = module
        try:
            spec.loader.exec_module(module)
        except BaseException:
            del sys.modules[_PACKAGE]
            raise
    return sys.modules[_PACKAGE + "._base"]


_base = _load_readers()
READERS, reader, Context, SourceError, Result = _base.READERS, _base.reader, _base.Context, _base.SourceError, _base.Result
run, http_get, utc_now = _base.run, _base.http_get, _base.utc_now
clip, mask_ids, short_hash, iso, parse_time = _base.clip, _base.mask_ids, _base.short_hash, _base.iso, _base.parse_time
proxies_from_env, sentence, ToolProblem = _base.proxies_from_env, _base.sentence, _base.ToolProblem
SOURCE_STATES, FACT_STATES, DETAIL_MAX = _base.SOURCE_STATES, _base.FACT_STATES, _base.DETAIL_MAX
LINK_CONFIDENCE, canonical_id, repo_ref = _base.LINK_CONFIDENCE, _base.canonical_id, _base.repo_ref

OBSERVATION_SCHEMA = "compound.system_inventory_observation.v0.2"
SOURCES_SCHEMA = "compound.system_inventory_sources.v0.1"
COLLECTOR = {"name": "compound-inventory-collect", "version": "0.2.0"}
EXIT_OK, EXIT_WRITE, EXIT_USAGE = 0, 1, 2


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


def normalize_link(link):
    """`{rel, to, confidence[, evidence][, observed_at]}`, or None when rel or to is missing.
    A confidence the contract does not know becomes the weakest one, `inferred`: a link never
    claims more than its reader knew. `evidence` is a short pointer (a file, a command name)."""
    if not isinstance(link, dict):
        return None
    rel, to = str(link.get("rel") or "").strip(), str(link.get("to") or "").strip()
    if not rel or not to:
        return None
    confidence = link.get("confidence") or "observed"
    out = {"rel": rel, "to": to, "confidence": confidence if confidence in LINK_CONFIDENCE else "inferred"}
    evidence = clip(link.get("evidence") or "")
    if evidence:
        out["evidence"] = evidence
    observed = parse_time(link.get("observed_at"))
    if observed:
        out["observed_at"] = iso(observed)
    return out


def normalize_resource(res, fallback_time):
    """Only the contract's fields, in their types. Returns (resource, number of facts dropped
    because their name is not lowercase letters, digits and _)."""
    out = {key: str(res.get(key) or "") for key in ("id", "kind", "provider", "label", "parent", "source")}
    out["label"] = out["label"] or out["id"]
    if out["parent"] == out["id"]:
        out["parent"] = ""
    observed = parse_time(res.get("observed_at"))
    if observed:  # only for sources that carry older evidence; otherwise the source's time counts
        out["observed_at"] = iso(observed)
    out["places"] = [str(p) for p in (res.get("places") or []) if str(p or "").strip()]
    out["links"] = [link for link in (normalize_link(l) for l in (res.get("links") or [])) if link]
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
            entry["state"], entry["detail"] = "skipped", str(row.get("note") or "Turned off in the sources file.")
            continue
        if only and sid != only:
            entry["state"], entry["detail"] = "skipped", "Not read this run: another single source was asked for."
            continue
        fn = READERS.get(row["provider"])
        if fn is None:
            entry["state"], entry["detail"] = "not_supported", \
                f"This collector has no reader for the provider '{row['provider']}'."
            continue
        started = time.monotonic()
        try:
            ctx = Context(row, runner, http, now, timeout_s, base_env)
            entry["ctx"], entry["observed_at"] = ctx, ctx.observed_at
            entry["resources"] = list(fn(ctx) or [])
            entry["state"], entry["detail"] = ctx.state, ctx.detail
        except SourceError as exc:
            entry["state"], entry["detail"] = exc.state, exc.detail
        except Exception:  # a reader bug must not stop the other sources
            entry["state"], entry["detail"] = "failed", "The reader for this source stopped on an unexpected error."
        entry["duration_ms"] = int((time.monotonic() - started) * 1000)
    for sid in order:
        ctx = results[sid]["ctx"]
        for hook in (ctx.after if ctx else []):
            try:
                hook(results)
            except Exception:
                results[sid]["detail"] = (results[sid]["detail"] + "; " if results[sid]["detail"] else "") + \
                    "the step that links this source to another one stopped on an unexpected error"
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
        parts = [entry["detail"].rstrip(". ")] if entry["detail"] else []
        if dupes:
            parts.append(f"{dupes} already reported by another source")
        if dropped:
            parts.append(f"{dropped} facts dropped: a fact name must be lowercase letters, digits and _")
        sources.append({"id": sid, "provider": entry["row"]["provider"], "state": entry["state"],
                        "detail": sentence("; ".join(parts)), "observed_at": entry["observed_at"],
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
