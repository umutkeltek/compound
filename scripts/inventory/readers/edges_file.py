"""edges_file: a nodes-and-edges JSON file another tool or a one-off scout wrote -> resources and links.

The file: {"observed_at"?: time, "nodes": [{id, kind, label, provider}], "edges": [{from, rel, to,
confidence?, evidence?}]}. The sources row says how to read it:

  path          the file
  kind_map      the file's kind -> an inventory kind (null: left out on purpose)
  relation_map  the file's relation -> an inventory relation (null: left out on purpose)
  same_as       the file's node id -> the id another reader emits for the same thing (an id in
                a file's own words, `<kind>:…`, is turned into the id that file's node gets)
  report_path   optional: a private JSON report (mode 0600) of what was left out, merged or looks
                like a duplicate, with labels, for the person who tunes the maps

Ids become canonical (domain:, hostname:, repo:, …; product:<id> for kind product); every other
node gets `<row vendor>:<kind>:<hash of its id in the file>`. `observed_at` is the file's own date
(its `observed_at` field, else the file's modification time). Confidence is carried through.
Nothing is dropped silently: a kind or relation the maps do not cover, and a link whose end was
not kept, is counted in the source detail and listed in the report.
"""

import collections
import json
import os
import re
from datetime import datetime, timezone

from ._base import (CANONICAL_KINDS, KINDS, PRODUCT_PREFIX, SourceError, canonical_id, iso, parse_time,
                    provider_token, reader, short_hash)


def read_graph(path):
    try:
        with open(path, encoding="utf-8") as handle:
            doc = json.load(handle)
        modified = os.stat(path).st_mtime
    except FileNotFoundError:
        raise SourceError("failed", "The nodes-and-links file does not exist.")
    except ValueError:
        raise SourceError("failed", "The nodes-and-links file could not be read: it is not in the expected format.")
    except OSError:
        raise SourceError("failed", "The nodes-and-links file cannot be read.")
    if not isinstance(doc, dict) or not isinstance(doc.get("nodes"), list) or not isinstance(doc.get("edges"), list):
        raise SourceError("failed", "The nodes-and-links file has no list of nodes and list of links.")
    stamped = parse_time(doc.get("observed_at"))
    return doc, iso(stamped or datetime.fromtimestamp(modified, timezone.utc))


def node_id(kind, scout_id, node, vendor):
    """The inventory id for one node of the file, or "" when it cannot have one. A node without a
    canonical form gets `<row vendor>:<kind>:<hash of its id in the file>`, so two files that use
    the same node id (and rows with the same `vendor`) give the same resource."""
    rest = scout_id.split(":", 1)[1] if ":" in scout_id else scout_id
    if kind == "product":
        return canonical_id("product", rest.strip())
    if kind in CANONICAL_KINDS:
        if kind == "store_app" and "/" not in rest:
            rest = f"{provider_token(node.get('provider'), 'unknown')}/{rest}"
        return canonical_id(kind, rest)
    return f"{provider_token(vendor, 'edges_file')}:{kind}:{short_hash(scout_id)}"


def same_as_id(value, vendor):
    """A `same_as` target: an id in a file's own words (`<kind>:…` with an inventory kind) is
    turned into the id that file's node gets; anything else is an inventory id, used as it is."""
    value = str(value or "").strip()
    kind = value.split(":", 1)[0]
    return node_id(kind, value, {}, vendor) if kind in KINDS and ":" in value else value


def label_key(kind, label):
    """Kind plus the label without a trailing note in brackets, for spotting likely duplicates."""
    text = re.sub(r"\s*\(.*$", "", str(label or "")).strip().lower()
    return (kind, text) if text else None


@reader("edges_file")
def read_edges_file(ctx):
    path = os.path.expanduser(str(ctx.require("path")))
    doc, observed_at = read_graph(path)
    kind_map, rel_map = ctx.row.get("kind_map") or {}, ctx.row.get("relation_map") or {}
    same_as = ctx.row.get("same_as") or {}
    stats = collections.Counter()
    unmapped_kinds, unmapped_rels = collections.Counter(), collections.Counter()
    ids, resources = {}, collections.OrderedDict()
    for node in doc["nodes"]:
        node = node if isinstance(node, dict) else {}
        scout_id, kind = str(node.get("id") or ""), str(node.get("kind") or "")
        if not scout_id:
            stats["nodes_without_id"] += 1
            continue
        if kind not in kind_map:
            unmapped_kinds[kind or "(none)"] += 1
            continue
        target = kind_map[kind]
        if target is None:
            stats["nodes_left_out"] += 1
            continue
        rid = same_as_id(same_as.get(scout_id), ctx.vendor) or node_id(target, scout_id, node, ctx.vendor)
        if not rid or (rid.startswith(PRODUCT_PREFIX) != (target == "product")):
            stats["nodes_without_usable_id"] += 1
            continue
        ids[scout_id] = rid
        if scout_id in same_as:
            stats["same_as"] += 1
        if rid in resources:
            stats["merged_in_file"] += 1
            continue
        res = ctx.resource(rid, target, str(node.get("label") or rid),
                           provider=str(node.get("provider") or "") or ctx.vendor)
        res["observed_at"] = observed_at
        res["_secondhand"] = True  # older evidence: a resource this run reads itself keeps its kind and label
        res["_stands_in"] = scout_id in same_as  # and the resource it stands for keeps them too
        resources[rid] = res
    for edge in doc["edges"]:
        edge = edge if isinstance(edge, dict) else {}
        rel = str(edge.get("rel") or "")
        if rel not in rel_map:
            unmapped_rels[rel or "(none)"] += 1
            continue
        if rel_map[rel] is None:
            stats["links_left_out"] += 1
            continue
        source_id, target_id = ids.get(str(edge.get("from") or "")), ids.get(str(edge.get("to") or ""))
        if source_id is None or target_id is None:
            stats["links_without_end"] += 1
            continue
        if source_id == target_id:
            stats["links_to_self"] += 1
            continue
        resources[source_id]["links"].append({
            "rel": rel_map[rel], "to": target_id, "confidence": edge.get("confidence") or "observed",
            "evidence": str(edge.get("evidence") or ""), "observed_at": observed_at})
        stats["links"] += 1
    stats["nodes"] = len(resources)
    ctx.detail = summary(len(doc["nodes"]), len(doc["edges"]), stats, unmapped_kinds, unmapped_rels)
    ctx.after.append(lambda results: report_duplicates(ctx, results, list(resources.values()), stats,
                                                       unmapped_kinds, unmapped_rels))
    return list(resources.values())


def summary(nodes_in, edges_in, stats, unmapped_kinds, unmapped_rels):
    parts = [f"Kept {stats['nodes']} of {nodes_in} nodes and {stats['links']} of {edges_in} links"]
    if unmapped_kinds:
        parts.append("kinds not in the map: " + ", ".join(f"{n} {c}" for n, c in unmapped_kinds.most_common(4)))
    if unmapped_rels:
        parts.append("relations not in the map: " + ", ".join(f"{n} {c}" for n, c in unmapped_rels.most_common(4)))
    for key, words in (("nodes_left_out", "nodes left out by the map"),
                       ("links_left_out", "links left out by the map"),
                       ("links_without_end", "links missing an end"),
                       ("links_to_self", "links to themselves"),
                       ("nodes_without_id", "nodes without an id"),
                       ("nodes_without_usable_id", "nodes without a usable id"),
                       ("merged_in_file", "nodes merged inside the file"),
                       ("same_as", "same_as matches")):
        if stats[key]:
            parts.append(f"{words}: {stats[key]}")
    return "; ".join(parts)


def report_duplicates(ctx, results, mine, stats, unmapped_kinds, unmapped_rels):
    """After every source ran: count resources that share kind and label with a resource of a
    different id (likely the same thing under two ids), and write the optional private report."""
    others = collections.defaultdict(set)
    for entry in results.values():
        for res in entry["resources"]:
            key = label_key(res.get("kind"), res.get("label"))
            if key and res.get("id"):
                others[key].add(res["id"])
    suspects = []
    for res in mine:
        key = label_key(res["kind"], res["label"])
        if key and len(others[key]) > 1:
            suspects.append({"kind": res["kind"], "label": res["label"], "ids": sorted(others[key])})
    if suspects:
        entry = results[ctx.id]
        entry["detail"] += f"; likely duplicates: {len(suspects)}"  # same kind and label, other id
    path = ctx.row.get("report_path")
    if path:
        write_report(os.path.expanduser(str(path)), {
            "source": ctx.id, "observed_at": ctx.observed_at, "counts": dict(sorted(stats.items())),
            "kinds_not_in_map": dict(unmapped_kinds.most_common()),
            "relations_not_in_map": dict(unmapped_rels.most_common()),
            "likely_duplicates": suspects})


def write_report(path, doc):
    tmp = f"{path}.tmp-{os.getpid()}"
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as handle:
        json.dump(doc, handle, indent=1, ensure_ascii=False)
        handle.write("\n")
    os.replace(tmp, path)
