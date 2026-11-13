"""repo_scan: what local git repositories say about where their code runs and what it uses.

Reads only files `git ls-files` lists, and only configuration: site and function config
(wrangler, amplify, fly, vercel, netlify, CNAME), compose files, CI workflows, build manifests
(package.json, requirements, pyproject, go.mod, Swift packages, Podfile, Xcode projects and
entitlements, StoreKit files) and the NAMES in env files and CI secrets. A value from an env
file, a compose environment or a CI file is never read into the output: each line is cut at the
first `=` or `:` before anything is kept. `has_remote` and `last_commit` come from local git
only (`git remote -v`, `git log -1`); nothing is fetched.

Row: `roots` (folders searched for repositories, `max_depth` deep, default 3), `relative_to`
(names and evidence are paths relative to it; default each root's parent), `product_command`
(optional; prints Compound's products as JSON, each with `product_id` and `surfaces` paths: a
repository holding a product's surface belongs to it) and `product_map` (optional; a repository
path, relative or absolute, -> one product id or a list). Resources: `repo`, `product`,
`deployment`, `datastore`, `external_service`, `store_app`, `hostname`; links: `code_in`,
`deployed_on`, `serves`, `uses`, `calls`, `built_by`, `sold_through`. Ids follow one grammar
(`<vendor>:<kind>:<hash of a natural key such as deployment:cloudflare-worker/<name>>`), so
another source that uses the same key and vendor gives the same resource.
"""

import collections
import json
import os
import plistlib
import re
import tomllib

from ._base import SourceError, canonical_id, parse_json, provider_token, reader, short_hash

MAX_FILES, MAX_BYTES = 9000, 350_000
SKIP_DIRS = {"node_modules", "vendor", "Pods", ".build", "DerivedData", "dist", "build", "out", ".next",
             ".venv", "venv", "site-packages", "Carthage", "coverage", "fixtures", "testdata", "__snapshots__"}
TEST_PATH = re.compile(r"(^|/)(tests?|Tests|__tests__|docs|examples?|fixtures|archive)/|\.test\.|_test\.|Tests?\.swift$")
APP_SKIP = re.compile(r"tests?|widget|watch|extension|intents|notification|share|clip", re.I)
ENV_NAME = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*$")
PLACEHOLDER_HOST = re.compile(r"(^|\.)(example\.(com|org|net)|localhost|invalid|test|local|internal)$")
HOST_RULE = re.compile(r"Host(?:SNI)?\(([^)]*)\)")
QUOTED = re.compile(r"[`'\"]([^`'\"]+)[`'\"]")
DATASTORE_IMAGES = (("timescale", "Postgres/TimescaleDB container"), ("pgvector", "Postgres+pgvector container"),
                    ("postgis", "Postgres container"), ("postgres", "Postgres container"), ("redis", "Redis container"),
                    ("valkey", "Redis container"), ("mongo", "MongoDB container"), ("mysql", "MySQL container"),
                    ("mariadb", "MySQL container"), ("minio", "MinIO container"), ("clickhouse", "ClickHouse container"))
DEPLOY_STEP = re.compile(r"wrangler[^\n]*(deploy|publish)|pages deploy|aws\s+(s3|amplify|cloudfront|ecs|lambda)|"
                         r"fly(ctl)?\s+deploy|vercel|netlify\s+deploy|docker\s+(push|buildx[^\n]*--push)|"
                         r"deploy-pages|fastlane|altool|notarytool|gh\s+release", re.I)
# (kind, service, pattern over a lowercased package, env or secret name). Public vendor names only.
SERVICES = [(kind, name, re.compile(pattern)) for kind, name, pattern in (
    ("external_service", "RevenueCat", r"revenuecat|purchases-ios|react-native-purchases"),
    ("external_service", "Stripe", r"\bstripe"), ("external_service", "Paddle", r"\bpaddle"),
    ("external_service", "LemonSqueezy", r"lemon.?squeezy"), ("external_service", "Superwall", r"superwall"),
    ("external_service", "PostHog", r"posthog"), ("external_service", "TelemetryDeck", r"telemetrydeck"),
    ("external_service", "Mixpanel", r"mixpanel"), ("external_service", "Amplitude", r"amplitude"),
    ("external_service", "Plausible", r"plausible"), ("external_service", "Sentry", r"sentry"),
    ("external_service", "Crashlytics", r"crashlytics"), ("external_service", "Firebase", r"\bfirebase"),
    ("external_service", "Supabase", r"supabase"), ("external_service", "Clerk", r"\bclerk"),
    ("external_service", "Auth0", r"auth0"), ("external_service", "Resend", r"\bresend\b|^resend_"),
    ("external_service", "SendGrid", r"sendgrid"), ("external_service", "Postmark", r"postmark"),
    ("external_service", "Mailgun", r"mailgun"), ("external_service", "OneSignal", r"onesignal"),
    ("external_service", "Mapbox", r"mapbox"), ("external_service", "Google Maps", r"googlemaps|google-maps"),
    ("external_service", "OpenAI", r"\bopenai\b|^openai"), ("external_service", "Anthropic", r"anthropic"),
    ("external_service", "Google Gemini", r"google-genai|@google/genai|google-generativeai|\bgemini"),
    ("external_service", "OpenRouter", r"openrouter"), ("external_service", "Groq", r"\bgroq"),
    ("external_service", "DeepSeek", r"deepseek"), ("external_service", "Mistral", r"mistral"),
    ("external_service", "ElevenLabs", r"elevenlabs"), ("external_service", "Replicate", r"replicate"),
    ("external_service", "fal.ai", r"fal-client|@fal-ai|^fal_key"), ("external_service", "Telegram", r"telegram"),
    ("external_service", "Slack", r"slack"), ("external_service", "Discord", r"discord"),
    ("external_service", "Twilio", r"twilio"), ("external_service", "Firecrawl", r"firecrawl"),
    ("external_service", "Cloudflare API", r"^cloudflare_|^cf_api"), ("external_service", "AWS", r"^aws_|@aws-sdk|boto3"),
    ("external_service", "GitHub API", r"^github_token|^gh_token|octokit"),
    ("external_service", "App Store Connect API", r"appstoreconnect|app-store-connect|app_store_connect|^asc_"),
    ("datastore", "Postgres", r"psycopg|asyncpg|^pg$|node-postgres|^postgres|pgx|^database_url$"),
    ("datastore", "MySQL", r"mysql|mariadb|pymysql"), ("datastore", "Redis", r"redis|ioredis|valkey"),
    ("datastore", "MongoDB", r"mongo"), ("datastore", "AWS S3", r"client-s3|^s3_|aws_s3"),
    ("datastore", "Cloudflare R2", r"^r2_"), ("datastore", "Turso/libSQL", r"libsql|turso"),
    ("datastore", "Neon", r"@neondatabase"), ("datastore", "Upstash", r"upstash"), ("datastore", "Qdrant", r"qdrant"),
)]
ENTITLEMENTS = (("healthkit", "external_service:apple-healthkit", "Apple HealthKit"),
                ("weatherkit", "external_service:apple-weatherkit", "Apple WeatherKit"),
                ("aps-environment", "external_service:apple-apns", "Apple Push Notification service"),
                ("applesignin", "external_service:sign-in-with-apple", "Sign in with Apple"))


def slug(text):
    return re.sub(r"[^a-z0-9.\-]+", "-", str(text).lower()).strip("-")


def names_only(lines):
    """The names in `NAME=value` / `NAME: value` / `export NAME=value` lines; values are cut off
    at the first `=` or `:` and never kept."""
    names = []
    for line in lines:
        text = line.strip()
        if not text or text.startswith("#"):
            continue
        text = text[len("export "):] if text.startswith("export ") else text
        text = text[2:] if text.startswith("- ") else text
        name = re.split(r"[=:]", text, maxsplit=1)[0].strip().strip("'\"")
        if ENV_NAME.match(name):
            names.append(name)
    return names


def strip_jsonc(text):
    out, i, n, in_string = [], 0, len(text), False
    while i < n:
        ch = text[i]
        if in_string:
            out.append(ch)
            if ch == "\\" and i + 1 < n:
                out.append(text[i + 1])
                i += 2
                continue
            in_string = ch != '"'
        elif ch == '"':
            in_string = True
            out.append(ch)
        elif text.startswith("//", i):
            i = text.find("\n", i) if text.find("\n", i) >= 0 else n
            continue
        elif text.startswith("/*", i):
            end = text.find("*/", i + 2)
            i = n if end < 0 else end + 2
            continue
        else:
            out.append(ch)
        i += 1
    return re.sub(r",(\s*[}\]])", r"\1", "".join(out))


def compose_services(text):
    """{service: {"image", "hosts", "env"}} from a compose file, without a YAML library: the
    `services:` block by indentation; environment entries kept as NAMES only."""
    services, lines = {}, text.splitlines()
    try:
        start = next(i for i, line in enumerate(lines) if re.match(r"^services:\s*(#.*)?$", line))
    except StopIteration:
        return services
    indent, current, in_env, env_indent = None, None, False, 0
    for line in lines[start + 1:]:
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        depth = len(line) - len(line.lstrip())
        if depth == 0:
            break
        key = re.match(r"^\s*([A-Za-z0-9._-]+):\s*(#.*)?$", line)
        if indent is None and key:
            indent = depth
        if depth == indent and key:
            current, in_env = key.group(1), False
            services[current] = {"image": "", "hosts": [], "env": []}
            continue
        if current is None:
            continue
        entry = services[current]
        if in_env and depth > env_indent:
            entry["env"] += names_only([line])
            continue
        in_env = False
        stripped = line.strip()
        if re.match(r"^environment:\s*$", stripped):
            in_env, env_indent = True, depth
        elif stripped.startswith("image:"):
            entry["image"] = stripped[len("image:"):].strip().strip("'\"").split("@")[0]
        for rule in HOST_RULE.findall(line):
            entry["hosts"] += QUOTED.findall(rule)
        caddy = re.match(r"^-?\s*['\"]?caddy(?:_\d+)?['\"]?\s*[:=]\s*['\"]?([^'\"\s]+)", stripped)
        if caddy:
            entry["hosts"] += [h.strip() for h in caddy.group(1).split(",") if "." in h]
    return services


class Scan:
    """Resources and links of one run, deduplicated by id."""

    def __init__(self, ctx, vendor):
        self.ctx, self.vendor = ctx, vendor
        self.resources = collections.OrderedDict()

    def node(self, key, kind, label, provider, rid=None):
        rid = rid or f"{self.vendor}:{kind}:{short_hash(key)}"
        if rid not in self.resources:
            self.resources[rid] = self.ctx.resource(rid, kind, label, provider=provider)
        return rid

    def link(self, holder, rel, to, evidence, confidence="observed"):
        links = self.resources[holder]["links"]
        if not any(l["rel"] == rel and l["to"] == to for l in links):
            links.append({"rel": rel, "to": to, "confidence": confidence, "evidence": evidence})


def find_repos(roots, max_depth):
    found = []
    for root in roots:
        stack = [(root, 0)]
        while stack:
            folder, depth = stack.pop()
            if os.path.exists(os.path.join(folder, ".git")):
                found.append(folder)
            if depth >= max_depth:
                continue
            try:
                entries = sorted(os.scandir(folder), key=lambda e: e.name, reverse=True)
            except OSError:
                continue
            for entry in entries:
                if entry.is_dir(follow_symlinks=False) and not entry.name.startswith(".") and entry.name not in SKIP_DIRS:
                    stack.append((entry.path, depth + 1))
    return sorted(set(found))


def surface_paths(surface, home):
    text = str(surface or "").replace("${HOME}", home).replace("$HOME", home)
    text = re.sub(r"^~(?=/)", home, text)
    match = re.search(r"(/[^\s'\"]+)", text)
    return os.path.normpath("/" + match.group(1).lstrip("/")) if match else ""


def products_by_repo(ctx, repos, relative_to):
    """{repo path: [product ids]} from the row's product_map and its product_command."""
    owners = collections.defaultdict(list)
    home = os.path.expanduser("~")
    for path, products in (ctx.row.get("product_map") or {}).items():
        path = os.path.expanduser(path)
        path = os.path.normpath(path if os.path.isabs(path) else os.path.join(relative_to, path))
        for product in [products] if isinstance(products, str) else list(products):
            if path in repos and product not in owners[path]:
                owners[path].append(product)
    mapped = set(owners)  # the person's own map wins over the product list
    command = ctx.row.get("product_command")
    if command:
        result = ctx.run(command, what="the product list command")
        if result.code != 0:
            raise SourceError("failed", "The product list command failed; repositories were not read.")
        data = parse_json(result.out, "product list")
        items = data.get("products") if isinstance(data, dict) else data
        for item in items if isinstance(items, list) else []:
            product = str((item or {}).get("product_id") or "") if isinstance(item, dict) else ""
            for surface in (item.get("surfaces") or []) if product else []:
                path = surface_paths(surface, home)
                holder = max((r for r in repos if path == r or path.startswith(r + os.sep)), key=len, default="")
                if holder and holder not in mapped and product not in owners[holder]:
                    owners[holder].append(product)
    return owners


def read_text(path):
    try:
        if os.path.getsize(path) > MAX_BYTES:
            return None
        with open(path, "rb") as handle:
            data = handle.read()
    except OSError:
        return None
    return None if b"\x00" in data[:4096] else data.decode("utf-8", "replace")


@reader("repo_scan")
def read_repo_scan(ctx):
    roots = [os.path.normpath(os.path.expanduser(str(r))) for r in ctx.require("roots")]
    vendor = provider_token(ctx.row.get("vendor"), "repo_scan")
    scan = Scan(ctx, vendor)
    repos = find_repos(roots, int(ctx.row.get("max_depth") or 3))
    if not repos:
        raise SourceError("failed", "No git repository was found under the folders the sources file names.")
    base = {r: os.path.normpath(os.path.expanduser(str(ctx.row.get("relative_to") or os.path.dirname(root))))
            for root in roots for r in repos if r == root or r.startswith(root + os.sep)}
    relative_to = os.path.normpath(os.path.expanduser(str(ctx.row.get("relative_to") or os.path.dirname(roots[0]))))
    owners = products_by_repo(ctx, set(repos), relative_to)
    unreadable = 0
    for path in repos:
        if not scan_repo(ctx, scan, path, os.path.relpath(path, base[path]), owners.get(path, [])):
            unreadable += 1
    kinds = collections.Counter(r["kind"] for r in scan.resources.values())
    ctx.detail = (f"Read {len(repos) - unreadable} of {len(repos)} repositories; "
                  f"{sum(1 for o in owners.values() if o)} belong to a known product; "
                  + ", ".join(f"{kind} {n}" for kind, n in sorted(kinds.items())))
    return list(scan.resources.values())


def scan_repo(ctx, scan, path, rel, products):
    listing = ctx.run(["git", "-C", path, "ls-files", "-z"], what="git")
    if listing.code != 0:
        return False
    files = [f for f in listing.out.split("\0") if f][:MAX_FILES]
    remotes = ctx.run(["git", "-C", path, "remote", "-v"], what="git")
    remote = next((line.split()[1] for line in (remotes.out or "").splitlines()
                   if remotes.code == 0 and len(line.split()) >= 2), "")
    ref = canonical_id("repo", remote) if remote else ""
    repo = ref or canonical_id("repo", f"local/{slug(rel)}")
    last = ctx.run(["git", "-C", path, "log", "-1", "--format=%cI"], what="git")
    scan.node("", "repo", ref[len("repo:"):] if ref else rel, ref.split(":", 1)[1].split("/")[0] if ref else "local",
              rid=repo)
    facts = scan.resources[repo]["facts"]
    facts["has_remote"] = ctx.fact(bool(ref), "bool", "local git remote list; nothing fetched")
    stamp = (last.out or "").strip()[:10] if last.code == 0 else ""
    facts["last_commit"] = (ctx.fact(stamp, "date") if re.match(r"^\d{4}-\d{2}-\d{2}$", stamp)
                            else ctx.unknown("not_supported", "the repository has no commit yet", "date"))
    holders = []
    for product in products:
        pid = canonical_id("product", str(product).strip())
        if pid:
            scan.node("", "product", pid[len("product:"):], "compound", rid=pid)
            scan.link(pid, "code_in", repo, rel)
            holders.append(pid)
    holders = holders or [repo]
    found = collections.defaultdict(set)  # (kind, key, label) -> evidence files, for package/env matches
    secret_names, env_names, apps = set(), set(), []
    # app projects first, so entitlements and StoreKit files link to this repository's apps
    files.sort(key=lambda f: (os.path.basename(f) not in ("project.pbxproj", "project.yml"), f))
    for name in files:
        parts = name.split("/")
        if any(p in SKIP_DIRS for p in parts[:-1]):
            continue
        base = parts[-1]
        is_env = base.startswith(".env") or base.endswith(".env") or ".env." in base
        if not (is_env or base in WANTED_NAMES or base.endswith(WANTED_SUFFIXES) or
                (name.startswith(".github/workflows/") and base.endswith((".yml", ".yaml"))) or
                re.match(r"(docker-)?compose[^/]*\.ya?ml$", base) or re.match(r"requirements[^/]*\.txt$", base)):
            continue
        text = read_text(os.path.join(path, name))
        if text is None:
            continue
        where = f"{rel}/{name}"
        if is_env:
            for env in names_only(text.splitlines()):
                env_names.add(env)
                match_services(found, env, where)
            continue
        read_config(scan, holders, repo, rel, name, base, text, where, found, secret_names, env_names, apps)
    for (kind, key, label), evidence in sorted(found.items()):
        good = sorted(e for e in evidence if not TEST_PATH.search(e.split("/", 1)[-1]))
        if good:
            target = scan.node(key, kind, label, "")
            for holder in holders:
                scan.link(holder, "uses", target, good[0])
    if secret_names:
        facts["secret_names"] = ctx.fact(len(secret_names), "count", "CI secret names: " + ", ".join(sorted(secret_names)))
    if env_names:
        facts["env_names"] = ctx.fact(len(env_names), "count", "environment variable names: " + ", ".join(sorted(env_names)))
    return True


WANTED_NAMES = {"wrangler.toml", "wrangler.json", "wrangler.jsonc", "amplify.yml", "amplify.yaml", "fly.toml",
                "vercel.json", "netlify.toml", "CNAME", "package.json", "pyproject.toml", "go.mod", "Package.swift",
                "Package.resolved", "Podfile", "project.pbxproj", "project.yml", "Caddyfile"}
WANTED_SUFFIXES = (".entitlements", ".storekit")


def match_services(found, token, where):
    low = token.lower()
    for kind, name, pattern in SERVICES:
        if pattern.search(low):
            found[(kind, f"{kind}:{slug(name)}", name)].add(where)


def deploy(scan, holders, key, label, provider, where):
    did = scan.node(key, "deployment", label, provider)
    facts = scan.resources[did]["facts"]
    facts.setdefault("live", scan.ctx.fact("not_known", "", "a repository file shows how it deploys, not whether it is live"))
    for holder in holders:
        scan.link(holder, "deployed_on", did, where)
    return did


def serve(scan, did, host, where):
    host = host.split("/")[0].replace("*", "").strip(".").lower()
    if host and "." in host and not PLACEHOLDER_HOST.search(host):
        scan.link(did, "serves", scan.node("", "hostname", host, "dns", rid=canonical_id("hostname", host)), where)


def read_config(scan, holders, repo, rel, name, base, text, where, found, secret_names, env_names, apps):
    if base in ("wrangler.toml", "wrangler.json", "wrangler.jsonc"):
        try:
            conf = tomllib.loads(text) if base.endswith(".toml") else json.loads(strip_jsonc(text))
        except ValueError:
            return
        kind = "cloudflare-pages" if conf.get("pages_build_output_dir") else "cloudflare-worker"
        worker = str(conf.get("name") or slug(rel))
        did = deploy(scan, holders, f"deployment:{kind}/{worker}", f"{kind} {worker}", "cloudflare", where)
        for section in [conf] + [v for v in (conf.get("env") or {}).values() if isinstance(v, dict)]:
            routes = list(section.get("routes") or []) + ([section["route"]] if section.get("route") else [])
            for route in routes:
                serve(scan, did, route.get("pattern", "") if isinstance(route, dict) else str(route), where)
            for kv in section.get("kv_namespaces") or []:
                binding = str((kv or {}).get("binding") or "")
                scan.link(did, "uses", scan.node(f"datastore:cf-kv/{worker}/{binding}", "datastore",
                                                 f"Workers KV {binding} ({worker})", "cloudflare"), where)
            for bucket in section.get("r2_buckets") or []:
                name_ = str((bucket or {}).get("bucket_name") or "")
                scan.link(did, "uses", scan.node(f"datastore:r2/{name_}", "datastore", f"R2 bucket {name_}", "cloudflare"), where)
            for db in section.get("d1_databases") or []:
                name_ = str((db or {}).get("database_name") or "")
                scan.link(did, "uses", scan.node(f"datastore:d1/{name_}", "datastore", f"D1 {name_}", "cloudflare"), where)
            for bound in section.get("services") or []:
                other = str((bound or {}).get("service") or "")
                if other:
                    scan.link(did, "calls", scan.node(f"deployment:cloudflare-worker/{other}", "deployment",
                                                      f"cloudflare-worker {other}", "cloudflare"), where)
    elif base in ("amplify.yml", "amplify.yaml"):
        deploy(scan, holders, f"deployment:aws-amplify/{slug(rel)}", f"AWS Amplify app built from {rel}", "aws", where)
    elif base == "fly.toml":
        app = re.search(r"^app\s*=\s*['\"]([^'\"]+)", text, re.M)
        deploy(scan, holders, f"deployment:fly/{app.group(1) if app else slug(rel)}", f"Fly.io app {app.group(1) if app else rel}", "fly", where)
    elif base in ("vercel.json", "netlify.toml"):
        host = "vercel" if base == "vercel.json" else "netlify"
        deploy(scan, holders, f"deployment:{host}/{slug(rel)}", f"{host} site built from {rel}", host, where)
    elif base == "CNAME" and "/" not in name.strip("/") or name.endswith("/public/CNAME"):
        did = deploy(scan, holders, f"deployment:github-pages/{slug(rel)}", f"GitHub Pages site from {rel}", "github", where)
        serve(scan, did, text.strip().splitlines()[0] if text.strip() else "", where)
    elif base == "Caddyfile":
        for site in re.findall(r"^([^\s{#][^{\n]*?)\s*\{", text, re.M):
            for host in re.findall(r"[a-z0-9.-]+\.[a-z]{2,}", site):
                did = deploy(scan, holders, f"deployment:caddy/{slug(rel)}", f"Caddy site from {rel}", "caddy", where)
                serve(scan, did, host, where)
    elif re.match(r"(docker-)?compose[^/]*\.ya?ml$", base):
        for service, entry in compose_services(text).items():
            for env in entry["env"]:
                env_names.add(env)
                match_services(found, env, where)
            if entry["hosts"]:
                did = deploy(scan, holders, f"deployment:compose/{service}", f"docker compose service {service}", "docker", where)
                for host in entry["hosts"]:
                    serve(scan, did, host, where)
            image = entry["image"].lower()
            hit = next(((k, label) for k, label in DATASTORE_IMAGES if k in image), None)
            if hit and not re.search(r"dev|test", name):
                ds = scan.node(f"datastore:{hit[0]}-container/{slug(rel)}", "datastore", f"{hit[1]} in {where}", "docker")
                for holder in holders:
                    scan.link(holder, "uses", ds, f"{where} service {service}")
    elif name.startswith(".github/workflows/"):
        title = re.search(r"^name:\s*['\"]?(.+?)['\"]?\s*$", text, re.M)
        label = f"GitHub Actions '{title.group(1) if title else os.path.basename(name)}'" + \
            (" (deploys)" if DEPLOY_STEP.search(text) else "")
        ci = scan.node(f"ci:{slug(rel)}/{os.path.basename(name)}", "external_service", label, "github-actions")
        scan.link(repo, "built_by", ci, where)
        for secret in re.findall(r"secrets\.([A-Za-z_][A-Za-z0-9_]*)", text):
            if secret != "GITHUB_TOKEN":
                secret_names.add(secret)
                match_services(found, secret, where)
    elif base in ("project.pbxproj", "project.yml"):
        pattern = r"PRODUCT_BUNDLE_IDENTIFIER\s*[=:]\s*\"?([A-Za-z0-9.\-]+)\"?"
        store = scan.node("external_service:apple-app-store", "external_service", "Apple App Store", "apple")
        for bundle in sorted(set(re.findall(pattern, text))):
            if bundle.endswith(".") or bundle.count(".") < 2 or APP_SKIP.search(bundle.split(".", 2)[-1]):
                continue
            app = scan.node("", "store_app", bundle, "apple", rid=canonical_id("store_app", f"apple/{bundle}"))
            if app not in apps:
                apps.append(app)
            scan.link(app, "deployed_on", store, where)
            for holder in holders:
                scan.link(holder, "sold_through", app, where)
        for package in re.findall(r"repositoryURL = \"([^\"]+)\"", text):
            match_services(found, package.rstrip("/").split("/")[-1].removesuffix(".git"), where)
    elif base.endswith(".entitlements"):
        try:
            plist = plistlib.loads(text.encode())
        except Exception:
            return
        for key, value in plist.items():
            for word, natural, label in ENTITLEMENTS:
                if word in key and value:
                    for app in apps or holders:
                        scan.link(app, "uses", scan.node(natural, "external_service", label, "apple"), where)
            if "icloud-container-identifiers" in key and isinstance(value, list):
                for container in value:
                    for app in apps or holders:
                        scan.link(app, "uses", scan.node(f"datastore:cloudkit/{container}", "datastore",
                                                         f"CloudKit container {container}", "apple"), where)
    elif base.endswith(".storekit"):
        for app in apps or holders:
            scan.link(app, "uses", scan.node("external_service:apple-storekit", "external_service",
                                             "Apple StoreKit in-app purchases", "apple"), where)
    else:
        read_manifest(base, text, where, found)


def read_manifest(base, text, where, found):
    packages = []
    if base == "package.json":
        try:
            doc = json.loads(text)
        except ValueError:
            return
        for section in ("dependencies", "devDependencies", "peerDependencies"):
            packages += list((doc.get(section) or {}) if isinstance(doc, dict) else [])
    elif base == "pyproject.toml":
        try:
            doc = tomllib.loads(text)
        except ValueError:
            return
        deps = list((doc.get("project") or {}).get("dependencies") or [])
        deps += list(((doc.get("tool") or {}).get("poetry") or {}).get("dependencies", {}).keys())
        packages += [re.split(r"[<>=!~\[; ]", str(d), maxsplit=1)[0] for d in deps]
    elif base.startswith("requirements"):
        packages += [re.split(r"[<>=!~\[; #]", line.strip(), maxsplit=1)[0] for line in text.splitlines()
                     if line.strip() and not line.strip().startswith(("-", "#"))]
    elif base == "go.mod":
        packages += re.findall(r"^\s*(?:require\s+)?([a-z0-9.\-]+\.[a-z]+/[\w.\-/]+)\s+v", text, re.M)
    elif base in ("Package.swift", "Package.resolved"):
        packages += [u.rstrip("/").split("/")[-1].removesuffix(".git")
                     for u in re.findall(r"(?:url:\s*|\"(?:location|repositoryURL)\"\s*:\s*)\"([^\"]+)\"", text)]
    elif base == "Podfile":
        packages += re.findall(r"^\s*pod\s+['\"]([^'\"]+)", text, re.M)
    for package in packages:
        if package:
            match_services(found, package, where)
