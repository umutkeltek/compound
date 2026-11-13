"""search_console: search properties through the person's own command -> `site`s under one `account`."""

import urllib.parse

from ._base import SourceError, reader, run_json, short_hash


def site_host(url):
    if url.startswith("sc-domain:"):
        return url[len("sc-domain:"):].strip().lower()
    return (urllib.parse.urlsplit(url).hostname or "").lower()


@reader("search_console")
def read_search_console(ctx):
    """Search properties through the command the row names -> `site`s under one `account`."""
    data = run_json(ctx, ctx.require("command"), "property list", "the search property listing tool")
    if isinstance(data, dict):
        entries = data.get("siteEntry") or data.get("sites") or data.get("properties") or []
    elif isinstance(data, list):
        entries = data
    else:
        raise SourceError("failed", "The property list could not be read: it is not in the expected format.")
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
