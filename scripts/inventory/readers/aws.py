"""aws: one `account` per profile from the identity check and the free tagging API listing."""

import collections
import re

from ._base import FACT_STATES, SourceError, clip, parse_json, reader, short_hash

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
