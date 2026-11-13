"""balance_http: one free HTTPS GET of a balance endpoint -> one `plan`. The token lives in memory
for that one request only."""

from ._base import MISSING, SourceError, as_number, json_path, parse_json, reader, short_hash


def read_token(ctx):
    """The token for one request, from the named environment variable or command. Never stored."""
    env_name, command = ctx.row.get("token_env"), ctx.row.get("token_command")
    if env_name:
        token = (ctx.env.get(env_name) or "").strip()
        if not token:
            raise SourceError("needs_sign_in", f"The environment variable {env_name}, which should hold the token, is not set.")
    elif command:
        result = ctx.run(command, what="the token command")
        lines = (result.out or "").strip().splitlines()
        if result.code != 0 or not lines:
            raise SourceError("needs_sign_in", "The token command gave no token; sign in to the password manager again.")
        token = lines[0].strip()
    else:
        raise SourceError("failed", "The sources file row names neither a token variable (token_env) nor a token command (token_command).")
    if any(ch.isspace() or ord(ch) < 32 for ch in token):
        raise SourceError("needs_sign_in", "The token has an unexpected shape.")
    return token


@reader("balance_http")
def read_balance_http(ctx):
    """A free GET balance endpoint -> one `plan` with `balance` read at `balance_path`."""
    url = str(ctx.require("url"))
    if not url.startswith("https://"):
        raise SourceError("failed", "The address in the sources file must start with https://.")
    balance_path = ctx.require("balance_path")
    token = read_token(ctx)
    scheme = ctx.row.get("auth_scheme", "Bearer")
    headers = {"Accept": "application/json",
               ctx.row.get("auth_header") or "Authorization": f"{scheme} {token}" if scheme else token}
    try:
        status, body = ctx.http_get(url, headers)
    except SourceError:
        raise
    except Exception:  # never let an exception message (which may quote a header) through
        raise SourceError("failed", "The request to the endpoint could not be sent.")
    finally:
        token = headers = None
    if status in (401, 403):
        raise SourceError("needs_sign_in", f"The endpoint refused the token (HTTP {status}); sign in again.")
    if status != 200:
        raise SourceError("failed", f"The endpoint answered with an error (HTTP {status}).")
    data = parse_json((body or b"").decode("utf-8", "replace"), "balance answer")
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
