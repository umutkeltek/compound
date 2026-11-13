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
- **Names stay private.** The sources file holds the person's own labels, profile names and
  host aliases, so it lives under `COMPOUND_HOME`, never in a repository. Real account ids
  are hashed into resource ids (`sha256`, first 12 hex); only the last 4 characters are kept,
  as the `account_tail` fact.

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

## Adding a provider

A reader is one function in `collect.py` registered by provider name, plus one row in the
sources file. It calls tools only through `ctx.run` (or `ctx.http_get`), so tests can replace
them:

```python
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

## Tests

```sh
python3 -I scripts/inventory/test_collect.py
```

The tests use fake command output and synthetic names only; they never call a real tool.
