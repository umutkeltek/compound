# Inventory collector

`collect.py` observes what a person runs and pays for — cloud accounts, servers, virtual
machines, domains, search properties, paid plans and prepaid balances — and writes one
observation file. Compound reads that file with
`compound system inventory refresh --collector-json FILE` and shows it with
`compound system inventory`.

The collector lives outside the Compound core on purpose. Compound itself makes no external
call and needs no credentials for the inventory: it only validates and shows what the
collector observed. The collector uses the person's own signed-in tools (`aws`, `ssh`, …),
as named in a private sources file.

```sh
python3 -I scripts/inventory/collect.py \
  --sources "$COMPOUND_HOME/config/inventory-sources.json" \
  --out /path/to/observation.json [--only SOURCE_ID] [--timeout-s 20] [--verbose]
```

Exit code 0 means the file was written. A source that failed is data, not an error: it
appears in `sources[]` with its state and a plain reason. Exit 2 means a bad argument or
sources file; exit 1 means the output could not be written. The file is written readable by
its owner only (mode 0600), through a temporary file and a rename.

## Rules

- **Secrets.** The collector never prints, copies or stores a key, token or password, and
  never opens a credentials file. It calls the tools' own listing commands. Error details
  keep one line, and any long token with a digit in it (account ids, key ids) is cut to its
  last 4 characters.
- **Money.** Only free calls. Cost Explorer (`aws ce`) charges per request, so it is never
  called: the cost fact says `costs_money` instead. If a call is not known to be free, it is
  not made and the fact records why.
- **Read-only.** Listing and status commands only. Nothing is started, stopped, created or
  changed on any provider or host.
- **Unknown is an answer.** A fact that could not be read has `value: null` and a state —
  `needs_sign_in`, `needs_owner`, `costs_money`, `not_supported` or `failed` — with a plain
  detail. A missing number is never written as 0. A fact with state `ok` always has a value.
- **Plain failure text.** A source's `detail` is one plain sentence a person who does not
  program understands ("The DNS listing tool did not answer within 20 seconds."): never an
  exception class, a command line or a home path. When a tool's own words help, they follow
  "it said:", cut to one line, with home paths shown as `~`. A reader names each tool by its
  purpose: `ctx.run(cmd, what="the DNS listing tool")`.
- **Names stay private.** The sources file holds the person's own labels, profile names and
  host aliases, so it lives under `COMPOUND_HOME`, never in a repository. Real account ids
  are hashed into resource ids (`sha256`, first 12 hex); only the last 4 characters are kept,
  as the `account_tail` fact.

## Links and ids (observation file v0.2)

The file is `compound.system_inventory_observation.v0.2`. Each resource may carry `links`, each
`{rel, to, confidence, evidence, observed_at}`:

- `rel` is a relation from Compound's inventory kinds file (`runs_on`, `uses`, `serves`, …).
- `to` is another resource id in the same file, or `product:<product id>` for a Compound product.
- `confidence` is `observed` (a command or config file shows it now), `declared` (a person or a
  written record says it) or `inferred` (concluded from a name or a match; `evidence` says from
  what). A value the contract does not know is written as `inferred`, never as more.
- `evidence` is a short pointer (a repository-relative file, a command name), never a value.
- `observed_at` (also allowed on a resource) is when it was really seen, for sources that carry
  older evidence; without it the source's own time counts.

Things addressed by a name get a canonical id, so two readers that see the same thing emit the
same id: `domain:<name>`, `hostname:<fqdn>`, `mailbox:<address>`, `alias:<address>`,
`repo:<host>/<owner>/<name>` (or `repo:local/<folder name>` without a remote),
`host:<label>`, `store_app:<store>/<app id>`. Everything else keeps `<provider>:<kind>:<opaque>`,
with real ids hashed. Domains moved to `domain:<name>` in v0.2 (they were
`cloudflare:zone:<hash>`), so the first import after the change shows each domain once as
removed and added.

## Sources file

`sources.example.json` shows the shape with made-up names. Every row has an `id` (unique, the
person's own name for the source) and a `provider` (the reader to use). Common options:

| Option | Meaning |
|---|---|
| `enabled` | `false` lists the source as `skipped`, with `note` as the reason |
| `label` | the person's name for the main resource; labels are never guessed |
| `places` | aliases a product's deployment place may use for the main resource |
| `timeout_s` | per-command timeout for this source (default `--timeout-s`) |
| `env_unset` | environment variables to drop for this source only, for example the proxy variables when a network proxy breaks TLS to a provider |
| `vendor` | the provider name shown on the resources, when it differs from the reader name |
| `id_key` | what the resource id is hashed from, when the default (alias or row id) may change |

## Readers

| Provider | Reads | Resources |
|---|---|---|
| `aws` | `aws sts get-caller-identity`, then `aws resourcegroupstaggingapi get-resources` (free, capped by `max_resources`) in `region` or the profile's default region | one `account` per row (`profile`) |
| `pve` | a PVE virtualization cluster: `ssh <ssh_alias> pvesh get /cluster/resources --output-format json` | one `host` per node, one `guest` per virtual machine or container |
| `ssh_host` | `ssh <ssh_alias>` running `uptime`, `df -P /`, and `/proc/meminfo` and `/proc/stat` where they exist | one `host` |
| `cloudflare` | the zone list, through the `command` the row names (the person's own API wrapper); `{page}` in the command pages through `result_info.total_pages`, up to `max_pages` | one `domain` per zone, under one `account` per account id |
| `search_console` | the property list, through the `command` the row names | one `site` per property, under one `account` for the row |
| `balance_http` | one free HTTPS GET; the token comes from `token_env` (an environment variable name) or `token_command` (a command that prints it); the balance is read at `balance_path`, the unit from `unit` or `unit_path`, an end date from `ends_on_path` | one `plan` |
| `json_file` | numbers another tool already publishes in a local `.json` or `.jsonl` file, by JSON path | one `plan` per `plans[]` entry (or per group) |
| `edges_file` | a nodes-and-links JSON file another tool or a one-off scout wrote, read through `kind_map`, `relation_map` and `same_as` in the row | one resource per kept node, with its links |
| `declared` | a private file of nodes, links and facts a person or an agent states, each with `by`, `on` and `evidence` | its nodes, with links and facts of confidence `declared` |

Paths such as `balance_infos[0].total_balance` or `providers.example.used` step into objects
by name and into lists by index (`[-1]` is the last item).

Notes on what each reader cannot see:

- `aws`: the tagging API lists only resources that carry or once carried tags, in one region.
  Month-to-date cost is `costs_money`; credits and their end date are `needs_owner` (the
  billing console only).
- `pve`: the hypervisor does not see disk use inside a virtual machine, so a VM's
  `disk_pct` is `not_supported`, never 0. Containers report it. A stopped guest reports only
  its status. Templates are not listed.
- `ssh_host`: CPU is measured as busy time over one second from `/proc/stat`; memory counts
  reclaimable cache as free. A host without `/proc` (for example macOS) reports those two as
  `not_supported`.
- `cloudflare`: the zone list has no renewal date, so `renews_on` is `not_supported`. When
  the row names a `search_console_source`, `has_search_property` is set by exact host match
  only (`sc-domain:example.com` or `https://example.com/` match the zone `example.com`;
  `https://www.example.com/` does not), and a `search_property` link points at the site. If
  that source failed, the fact carries its state; if it was skipped, the fact is left out.
  `account_labels` maps the last 4 characters of an account id to the person's label.
- `search_console`: `last_data_day` needs a search analytics query, which is not run.
- `balance_http`: the token lives only in memory for the one request. It is never written to
  the observation file, a log or an error detail; an error raised while sending the request
  is reported by its type only. HTTP 401 or 403 is `needs_sign_in`. A balance the endpoint
  does not give (`null`) is `not_supported`, never 0. Only `https://` URLs are accepted.
- `json_file`: each `plans[]` entry picks either one object (`at`) or the last entry that
  matches `match` in a list (`items_path`, or the whole `.jsonl` file); `group_by` makes one
  plan per group (when there is more than one, each label ends in `#` and a short hash of
  the group key, never the key itself).
  `facts` maps a fact name to a path (or `{"path", "unit"}`). With `observed_at_path` each
  fact carries the entry's own time; with `max_age_hours` an older entry is reported as
  `failed` with its age instead of an outdated number.
- `edges_file`: the file is `{"observed_at"?, "nodes": [{id, kind, label, provider}], "edges":
  [{from, rel, to, confidence?, evidence?}]}`. `kind_map` and `relation_map` translate the file's
  words into inventory kinds and relations (`null` leaves one out on purpose); `same_as` maps a
  node id to the id another reader emits for the same thing. Ids become canonical; a node of kind
  `product` becomes `product:<product id>` and carries the product's own links. Every other node
  gets `<provider>:<kind>:<hash>`. `observed_at` on each resource and link is the file's own date
  (its `observed_at`, else its modification time); confidence is carried through. A kind or
  relation the maps do not cover, a link whose end was not kept, and a link to itself are counted
  in the source detail, never dropped silently; with `report_path` a private report (mode 0600)
  names them, with resources that share kind and label under another id (likely duplicates).
- `declared`: the file (row `path`; keep it mode 0600 under `COMPOUND_HOME`, never in a
  repository) is `{"schema_version": "compound.system_inventory_declared.v0.1", "nodes": [{id,
  kind, label, by, on, evidence}], "links": [{from, rel, to, by, on, evidence}], "facts":
  [{resource, name, value, unit, by, on, evidence}]}`. An entry without who (`by`), when (`on`)
  and from what (`evidence`, a pointer, never a value) is left out and counted; so is a link or
  fact on an id that is not a node of the file. A declared fact never overwrites one observed in
  the same run: when they disagree, the detail names the fact and the resource, and the observed
  value is kept; a declared fact fills one nobody observed, with who, when and evidence in its
  detail. Write only what a person or a written record actually says; never a guess.

Fact names must be lowercase letters, digits and `_`; the collector drops any other name and
says so in the source detail, so one bad row cannot make Compound refuse the whole file.

## One resource per id: merging

When two sources report the same id, the collector keeps one resource. What this run read
itself is the base; older evidence from a file (`edges_file`) and declared statements
(`declared`) merge into it: their links are added (one per relation and target, the strongest
confidence wins), and their facts fill only what the base lacks or reported as not known. A
resource or link whose kind or relation Compound's inventory kinds file does not serve is dropped
and counted, because Compound refuses a whole file over one; `readers/_base.py` keeps the list,
so a new kind or relation goes into the kinds file and there.

## Adding a provider

A reader is one module in `readers/` (for example `readers/example_cli.py`) holding one
function registered by provider name, plus one row in the sources file. Every module in
`readers/` whose name does not start with `_` is loaded on start, so adding a reader changes no
shared file. The shared pieces (`run`, `http_get`, `Context`, `SourceError`, the registry and the
parsing helpers) live in `readers/_base.py`; `collect.py` runs the readers and writes the file.
A reader calls tools only through `ctx.run` (or `ctx.http_get`), so tests can replace them:

```python
from ._base import SourceError, clip, parse_json, reader, short_hash


@reader("example_cli")
def read_example_cli(ctx):
    result = ctx.run(["example-cli", "projects", "list", "--json"])
    if result.code != 0:
        raise SourceError("needs_sign_in" if "login" in result.err else "failed", clip(result.err))
    rows = parse_json(result.out, "project list")
    return [ctx.resource(f"example:app:{short_hash(r['id'])}", "app", r["name"],
                         facts={"status": ctx.fact(r["state"]),
                                "cost_month": ctx.unknown("needs_owner", "billing is not listed")})
            for r in rows]
```

Then add `{"id": "example-projects", "provider": "example_cli", "label": "…"}` to the sources
file and a test with fake output in `test_collect.py`. The resource `kind` must exist in
Compound's inventory kinds file; facts it does not list are kept and shown under "other".

## Running it every day (prepared, not installed)

Nothing here is installed by the repository. This is a generic example a person can copy.
Save it as an executable file, for example `inventory-daily`, and set `INVENTORY_REPO` (or
edit `REPO`) to the folder that holds this repository:

```sh
#!/bin/sh
# inventory-daily: collect, then import into Compound. Safe to run with nobody present.
# Exit: 0 imported | 75 another run holds the lock | 142 the collector hit the time limit
#       1 or 2 the collector failed (nothing imported) | other: the import failed.
set -u
: "${COMPOUND_HOME:=$HOME/.compound}"
export COMPOUND_HOME
REPO="${INVENTORY_REPO:-/path/to/compound}"                # holds scripts/inventory/collect.py
SOURCES="${INVENTORY_SOURCES:-$COMPOUND_HOME/config/inventory-sources.json}"
COMPOUND_BIN="${COMPOUND_BIN:-compound}"
OBS="${TMPDIR:-/tmp}/compound-inventory-observation.json"  # written mode 0600, replaced each run
LOG="$COMPOUND_HOME/logs/inventory-daily.log"
LOCKFILE="$COMPOUND_HOME/locks/inventory-daily.lock"
LIMIT_S="${INVENTORY_LIMIT_S:-900}"                        # whole collection; each command has its own timeout too
stamp() { date -u +%Y-%m-%dT%H:%M:%SZ; }
mkdir -p "$COMPOUND_HOME/logs" "$COMPOUND_HOME/locks" || exit 1

if [ "${INVENTORY_DAILY_LOCKED:-}" != 1 ]; then            # take the lock without waiting, run again inside it
  case "$(uname -s)" in
    Darwin|*BSD) INVENTORY_DAILY_LOCKED=1 lockf -s -t 0 "$LOCKFILE" "$0" ;;
    *)           INVENTORY_DAILY_LOCKED=1 flock -n -E 75 "$LOCKFILE" "$0" ;;
  esac
  code=$?
  [ "$code" -eq 75 ] && echo "$(stamp) skipped: another run holds the lock" >>"$LOG"
  exit "$code"
fi

{
  echo "$(stamp) start"
  perl -e 'alarm shift; exec @ARGV or die "cannot start: $!\n"' "$LIMIT_S" \
    python3 -I "$REPO/scripts/inventory/collect.py" --sources "$SOURCES" --out "$OBS" --verbose
  code=$?
  if [ "$code" -eq 0 ]; then
    "$COMPOUND_BIN" system inventory refresh --collector-json "$OBS"
    code=$?
  fi
  echo "$(stamp) end exit=$code"
  exit "$code"
} >>"$LOG" 2>&1
```

Why it is safe unattended: the lock is taken without waiting, so a second run exits 75 at once
instead of piling up; every command runs without a terminal or stdin, `ssh` runs in batch
mode, and each command has its own timeout, with `perl`'s alarm as the limit for the whole
collection; a failed collection imports nothing; every run appends a start line, the
collector's one-line-per-source report and an end line with the exit code to the log.
Compound refuses an observation older than the one it holds, so a late run cannot replace a
newer snapshot.

What a person must install to run it daily:

1. Python 3 (standard library only), `perl`, and `lockf` (macOS, BSD) or `flock` (Linux).
   These ship with macOS and most Linux systems.
2. The `compound` binary on the job's `PATH` (or `COMPOUND_BIN` set to its full path), for
   the import.
3. Every tool the sources file names, signed in so it works without a prompt: for example
   `aws` profiles with a valid sign-in, an ssh key the job can use without a passphrase
   prompt (an agent socket or a key file), and the DNS and search wrappers.
4. A scheduler entry, which is the part that installs anything:
   - macOS: a LaunchAgent in `~/Library/LaunchAgents/` with `ProgramArguments` set to the
     script, `StartCalendarInterval` set to a daily time, and `EnvironmentVariables` giving a
     `PATH` that includes the tools' folders (launchd starts with a minimal `PATH` and does
     not read the shell profile); load it with `launchctl bootstrap gui/$(id -u) <plist>`.
   - Linux: a systemd user timer, or a cron line such as `15 6 * * * /path/to/inventory-daily`.
5. Tokens for `balance_http` sources: a scheduled job does not read the shell profile, so a
   `token_env` variable is usually not set there. Use `token_command` with a password
   manager command that works unattended. Never write a token into the job definition.

## Tests

```sh
python3 -I scripts/inventory/test_collect.py
```

The tests use fake command output and synthetic names only; they never call a real tool.
