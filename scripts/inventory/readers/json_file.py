"""json_file: numbers another tool already publishes in a local JSON or JSONL file -> `plan`s."""

import collections
import json
import os

from ._base import DEFAULT_UNITS, MISSING, SourceError, iso, json_path, parse_json, parse_time, reader, short_hash


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
        entries = pick_entries(doc, spec)
        for key, item in entries:
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
            suffix = f" #{short_hash(key)[-4:]}" if key and len(entries) > 1 else ""  # tell groups apart
            resources.append(ctx.resource(f"{vendor}:plan:{short_hash(ctx.row.get('id_key') or ctx.id, label, key)}",
                                          "plan", label + suffix, facts=facts, provider=vendor,
                                          places=spec.get("places")))
    return resources
