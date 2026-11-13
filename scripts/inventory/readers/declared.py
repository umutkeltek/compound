"""declared: what a person or an agent states, from a private file -> resources, links and facts
with confidence `declared`.

The file (row `path`, mode 0600, never in a repository):

  {"schema_version": "compound.system_inventory_declared.v0.1",
   "nodes": [{"id", "kind", "label", "provider"?, "by", "on", "evidence"}],
   "links": [{"from", "rel", "to", "by", "on", "evidence"}],
   "facts": [{"resource", "name", "value", "unit"?, "by", "on", "evidence"}]}

Every entry says who states it (`by`), when (`on`, a date or time) and from what (`evidence`, a
pointer, never a value); an entry without all three is left out and counted. A link's `from` and
a fact's `resource` must be a node of the file, so each one has a kind and a label. Ids of kinds
with a canonical form are made canonical; other ids must be the ids another reader emits.

Declared statements merge into what this run observed: a declared fact never overwrites an
observed one (a disagreement is named in this source's detail); it fills a fact nobody observed.
"""

import collections
import json
import os

from ._base import CANONICAL_KINDS, SourceError, canonical_id, iso, parse_time, reader

DECLARED_SCHEMA = "compound.system_inventory_declared.v0.1"


def load(path):
    try:
        with open(path, encoding="utf-8") as handle:
            doc = json.load(handle)
    except FileNotFoundError:
        raise SourceError("failed", "The declared file does not exist.")
    except ValueError:
        raise SourceError("failed", "The declared file could not be read: it is not in the expected format.")
    except OSError:
        raise SourceError("failed", "The declared file cannot be read.")
    if not isinstance(doc, dict) or doc.get("schema_version") not in (None, DECLARED_SCHEMA):
        raise SourceError("failed", f"The declared file is not a {DECLARED_SCHEMA} file.")
    return doc


def stated(entry):
    """(when, detail) for an entry that says by, on and evidence; None otherwise."""
    if not isinstance(entry, dict):
        return None
    by, on, evidence = (str(entry.get(key) or "").strip() for key in ("by", "on", "evidence"))
    when = parse_time(on if "T" in on else f"{on}T00:00:00Z")
    if not by or not when or not evidence:
        return None
    return iso(when), f"declared by {by} on {on}: {evidence}"


@reader("declared")
def read_declared(ctx):
    doc = load(os.path.expanduser(str(ctx.require("path"))))
    left = collections.Counter()
    nodes, ids = collections.OrderedDict(), {}
    for entry in doc.get("nodes") or []:
        said = stated(entry)
        if said is None:
            left["without by, on or evidence"] += 1
            continue
        raw, kind = str(entry.get("id") or ""), str(entry.get("kind") or "")
        canonical = (kind in CANONICAL_KINDS or kind == "product") and raw.startswith(f"{kind}:")
        rid = canonical_id(kind, raw[len(kind) + 1:]) if canonical else raw  # another reader's id stays as it is
        if not rid or not kind:
            left["nodes without id or kind"] += 1
            continue
        ids[raw] = rid
        if rid not in nodes:
            res = ctx.resource(rid, kind, str(entry.get("label") or rid),
                               provider=str(entry.get("provider") or "") or ctx.vendor)
            res["observed_at"], res["_declared"] = said[0], True
            nodes[rid] = res
    for entry in doc.get("links") or []:
        said = stated(entry)
        if said is None:
            left["without by, on or evidence"] += 1
            continue
        holder = nodes.get(ids.get(str(entry.get("from") or "")))
        if holder is None:
            left["links from an id that is not a node of the file"] += 1
            continue
        to = str(entry.get("to") or "")
        holder["links"].append({"rel": str(entry.get("rel") or ""), "to": ids.get(to, to), "confidence": "declared",
                                "evidence": str(entry.get("evidence")), "observed_at": said[0]})
    for entry in doc.get("facts") or []:
        said = stated(entry)
        if said is None:
            left["without by, on or evidence"] += 1
            continue
        holder = nodes.get(ids.get(str(entry.get("resource") or "")))
        if holder is None or not isinstance(entry.get("name"), str):
            left["facts on an id that is not a node of the file"] += 1
            continue
        holder["facts"][entry["name"]] = {"value": entry.get("value"), "unit": str(entry.get("unit") or ""),
                                          "state": "ok", "detail": said[1], "observed_at": said[0]}
    ctx.detail = (f"Declared {len(nodes)} nodes, {sum(len(r['links']) for r in nodes.values())} links and "
                  f"{sum(len(r['facts']) for r in nodes.values())} facts")
    if left:
        ctx.detail += "; left out: " + ", ".join(f"{n} {why}" for why, n in left.items())
    return list(nodes.values())
