"""cloudflare: zones with their plan, through the person's own API command -> `domain`s under
their `account`; optionally links each domain to its search property."""

import collections
import re

from ._base import FACT_STATES, SourceError, canonical_id, clip, reader, run_json, short_hash

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
            rid = canonical_id("domain", name) or f"cloudflare:zone:{short_hash(zone.get('id') or name)}"
            domain = ctx.resource(rid, "domain", name or "zone",
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
