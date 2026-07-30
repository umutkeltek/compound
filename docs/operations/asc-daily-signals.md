# ASC Daily Signals

Compound does not own App Store Connect fetching. ASC ingestion is a rented edge:
any collector can produce ASC-shaped output, and Compound only normalizes that
output into product signals through existing product-loop doors.

Keep private app IDs, vendor IDs, credentials, and account-specific mappings out
of tracked public files. Store them under `COMPOUND_HOME` or another private
runtime location.

Minimal private config shape:

```json
{
  "apps": [
    {
      "product_id": "product:example",
      "asc_app_id": "123456789",
      "surface": "app-store-page",
      "vendor": "12345678",
      "metrics": [
        "units",
        "developer_proceeds_usd",
        "impressions",
        "product_page_views",
        "product_page_conversion_rate"
      ]
    }
  ]
}
```

Dry run against a saved ASC snapshot:

```bash
bun tools/asc_daily_signals.ts \
  --config "${COMPOUND_HOME:-${HOME}/.compound}/private/asc-products.json" \
  --asc-json fixtures/product/asc-daily-signals.json \
  --json
```

Live run with any command that prints ASC daily JSON:

```bash
COMPOUND_ASC_DAILY_COMMAND='asc insights daily --app {app_id} --vendor {vendor} --date {date} --output json'

bun tools/asc_daily_signals.ts \
  --config "${COMPOUND_HOME:-${HOME}/.compound}/private/asc-products.json" \
  --asc-command "${COMPOUND_ASC_DAILY_COMMAND}" \
  --apply \
  --json
```

## Preflight

Before applying live signals or loading a scheduler, run preflight against the
private mapping and read-only ASC command:

```bash
bun tools/asc_preflight.ts \
  --config "${COMPOUND_HOME:-${HOME}/.compound}/private/asc-products.json" \
  --asc-command "${COMPOUND_ASC_DAILY_COMMAND}" \
  --json
```

Preflight checks that the private config parses, every mapped app has a
registered Compound product, the optional ASC command smoke returns JSON, and
scheduler load/unload commands are available. The JSON report includes
operator-only `actions[]` for private config, credential smoke, scheduler load,
and first signal apply. Each action carries prerequisites, expected validation,
rollback, and stop conditions. Preflight does not apply ProductSignals or load a
scheduler.

## Sales-Series JSONL Bridge

If an existing collector, cron job, export script, or data warehouse already
writes App Store Connect sales rows as JSONL, Compound should not fetch the same
source a second time. Use the bridge to map private product config plus existing
sales rows into the existing `compound product signal --asc-sales-series`
adapter.

```bash
bun tools/asc_sales_series_bridge.ts \
  --config "${COMPOUND_HOME:-${HOME}/.compound}/private/asc-products.json" \
  --sales-series "${ASC_SALES_SERIES_JSONL}" \
  --change-event "Describe the product or App Store change being evaluated." \
  --before 2026-07-01/2026-07-07 \
  --after 2026-07-08/2026-07-14 \
  --metric units,developer_proceeds_usd \
  --json
```

Add `--apply` only when the intended target is the current Compound runtime. For
tests and rehearsals, use a temporary `COMPOUND_HOME` and pass a temp-built
`--compound-bin`.

The bridge does not call ASC, install a scheduler, create a metrics store, or
assume any specific upstream collector. It only plans or invokes the existing
product-signal door.

When run with `--apply --json`, the report includes bounded `apply_results[]`
for each invoked product-signal command: `product_id`, `asc_app_id`, `metric`,
`signal_count`, and `transition_count`. These counts prove the generic
product-signal door accepted source rows without exposing private collector
output bodies.

Regression proof:

```bash
bun test tools/asc_sales_series_bridge.test.ts
```

The apply regression builds a temporary `compound`, uses a temporary
`COMPOUND_HOME`, runs the bridge twice with `--apply`, and verifies
ProductSignal ledger idempotency.

## Funnel-Series JSONL Bridge

If an existing collector already writes App Store Connect funnel rows as
JSONL (impressions, product_page_views, first_time_downloads, proceeds_usd,
paying_users, each day annotated with a `coverage` object), Compound should
not fetch that source a second time either. Use this bridge to map the same
private product config plus existing funnel rows into the
`compound product signal --asc-funnel-series` adapter.

Two data traps shape this adapter and are load-bearing, not incidental:

- **Map by `asc_app_id`, never by display name.** The row schema this bridge
  reads has no name-based matching path at all: the Go row type
  (`internal/product/asc_funnel_series.go`) does not even decode a name/slug
  field, and the bridge's own row filter (`rowsForApp` in
  `tools/asc_funnel_series_bridge.ts`) matches strictly on `asc_app_id`.
- **`coverage.complete_funnel` gates every metric.** On days where it is
  false (or absent, for rows produced before the collector started emitting
  it), `impressions` and `product_page_views` read as `0` because they are
  missing, not because nothing happened. The Go adapter aggregates only
  `coverage.complete_funnel == true` rows and carries the contributing row
  count through as `asc:funnel_series:before_complete_funnel_rows:N` /
  `..._after_complete_funnel_rows:N` evidence. The bridge itself pre-checks
  the same condition per app/window so a "rows exist but none have confirmed
  coverage" app is skipped with a specific, honest reason
  (`no_complete_funnel_rows_in_before_window` /
  `..._after_window`) rather than silently producing a deflated rate or
  failing the Go call unpredictably.

`first_time_downloads` can exceed `product_page_views` on the same day
because iOS allows installing directly from the search card. The derived
`product_page_conversion_rate` metric can therefore legitimately read above
1.0; the adapter does not treat that as an error, and records a confounder
note explaining why on every signal it produces for that metric.

```bash
bun tools/asc_funnel_series_bridge.ts \
  --config "${COMPOUND_HOME:-${HOME}/.compound}/private/asc-products.json" \
  --funnel-series "${ASC_FUNNEL_SERIES_JSONL}" \
  --change-event "Describe the product or App Store change being evaluated." \
  --before 2026-07-01/2026-07-07 \
  --after 2026-07-08/2026-07-14 \
  --metric product_page_conversion_rate,impressions,first_time_downloads \
  --json
```

Add `--apply` only when the intended target is the current Compound runtime.
For tests and rehearsals, use a temporary `COMPOUND_HOME` and pass a
temp-built `--compound-bin`, same as the sales-series bridge.

Regression proof:

```bash
bun test tools/asc_funnel_series_bridge.test.ts
```

## Acquisition Report Bridge

Sales and funnel rows say how many downloads happened. Neither says where a
download came from, so neither can separate installs a campaign paid for from
installs the store produced on its own. Without that split, "downloads went
up" and "spend went up" are the same number, and no store-listing change can
be evaluated against it.

App Store Connect answers this in the **App Downloads Detailed** analytics
report, which carries `Source Type`, `Source Info` and `Campaign` columns
alongside `Counts`. Export it and feed it to
`compound product signal --asc-acquisition`. As with every adapter in this
family, Compound reads an existing export and never fetches the report itself.

Retrieving the export is a four-hop walk, because the Analytics Reports API
addresses report data by instance rather than by date:

```bash
# 1. find the ONGOING request for the app
asc analytics requests --app "$APP_ID" --output json

# 2. list reports on it and pick "App Downloads Detailed"
ASC_TIMEOUT=180s asc analytics view --request-id "$REQUEST_ID" --output json

# 3. list that report's instances, then read each one's processingDate
ASC_TIMEOUT=120s asc analytics reports links --report-id "$REPORT_ID" --paginate --output json
ASC_TIMEOUT=90s asc analytics instances view --instance-id "$INSTANCE_ID" --output json

# 4. download the instance (gzipped TSV) and decompress
ASC_TIMEOUT=120s asc analytics download \
  --request-id "$REQUEST_ID" --instance-id "$INSTANCE_ID" --output downloads.gz
gzip -dc downloads.gz > downloads.tsv
```

Two tooling traps in that walk, both silent:

- `asc analytics view` exceeds the default request timeout on apps with many
  provisioned reports and fails as `context deadline exceeded`, which reads
  like a network fault rather than a timeout. Set `ASC_TIMEOUT` explicitly.
- On `asc analytics download`, `--output` is the **destination filename**, not
  the format flag it is on every other subcommand. Passing `--output json`
  writes a gzip file literally named `json`.

Two data traps shape the adapter itself and are load-bearing, not incidental:

- **Only first-time downloads are acquisition.** The report also carries
  `Redownload`, `Auto-update` and `Manual update` rows, and on ordinary days
  the update rows outnumber the acquisition rows several times over. An
  adapter that summed `Counts` without filtering would report update traffic
  as growth, and would spike on exactly the days a release shipped. The Go
  adapter (`internal/product/asc_acquisition.go`) filters on `Download Type`
  and carries the contributing row count through as
  `asc:acquisition:before_first_time_download_rows:N` evidence.
- **Paid is decided by `Campaign`, never by `Source Type`.** Campaign-driven
  installs are reported under the same `App Store search` source type as
  organic search installs and are distinguished only by a populated
  `Campaign`. Splitting on `Source Type` would classify every paid install as
  organic and report a paid share that does not move no matter what is spent.

Supported metrics: `first_time_downloads`, `paid_first_time_downloads`,
`organic_first_time_downloads`, and the derived ratio `paid_download_share`
(default). The three campaign-dependent metrics **fail closed** on an export
with no `Campaign` column rather than reporting everything as organic;
`first_time_downloads` still works against such a reduced export.

```bash
compound product signal \
  --asc-acquisition downloads.tsv \
  --product "$PRODUCT_ID" \
  --app-id "$APP_ID" \
  --before 2026-07-01/2026-07-07 \
  --after 2026-07-08/2026-07-14 \
  --change-event "Describe the product or App Store change being evaluated." \
  --metric paid_download_share
```

A window containing no first-time download rows is an error, not a zero: a
truncated export must not be readable as a collapse in acquisition.

**This report attributes downloads, never spend.** It answers "is this growth
organic or bought?" It cannot answer "what did it cost?" — cost per install
requires the separate Apple Ads API, whose credentials are distinct from the
App Store Connect API key.

Regression proof:

```bash
go test ./internal/product/ -run TestASCAcquisition
```

## From product signal to release authorization

`compound release` exports the Compound repository. It is not an application
release command. Product releases stay on the product loop:

1. Import the product's project and release contract so bundle, store app,
   version, and build are Compound-owned claims.
2. Create a release authorization plan from the verified candidate:

   ```bash
   compound product release-plan \
     --product product:example \
     --change "Ship the verified bounded change." \
     --candidate commit:0000000000000000000000000000000000000000 \
     --rollback "Restore the previous store version." \
     --metric app_store_connect_units
   ```

3. Inspect the generated review card, complete its named checks and evidence,
   then record the decision with `compound product release-review <review.yaml>`.

4. Turn an `authorize_release` result into a tier-3 operator-held work preview:

   ```bash
   compound product release-work-plan \
     --reviewed "${COMPOUND_HOME}/reviews/product-releases/<reviewed>.json" \
     --actor operator:<name>
   ```

   This records a WorkEnvelope create-plan with the exact product, destination,
   candidate, rollback, checks, authorization evidence, and post-release metric.
   It does not admit, claim, dispatch, or execute the work. Admit it explicitly
   with `compound work admit-plan --plan <create-plan.json>` only after the
   execution policy and provider adapter are ready.

5. After a separately governed external action emits a completed
   `outward_delta` Receipt, import the planned ProductSignal through the normal
   product signal door.

6. Close the product loop with the exact authorization, receipt, and signal:

   ```bash
   compound product release-observe \
     --reviewed "${COMPOUND_HOME}/reviews/product-releases/<reviewed>.json" \
     --execution rec_<id> \
     --signal sig_<id>
   ```

The review can authorize exactly one product identity and candidate. It never
uploads, submits, publishes, changes price, or mutates App Store Connect.
Execution remains a separate governed WorkEnvelope. Observation fails closed
unless the Receipt is linked to an allowing PolicyDecision for that exact work,
the signal belongs to the same product, its metric matches the plan, and its
timestamp follows execution.

## Scheduling

Scheduling is deployment-specific. Use cron, systemd timers, launchd, GitHub
Actions, a hosted worker, or any other scheduler that can run the same command
with private config and an ASC-producing command available. The bundled launchd
plist is only a macOS example and collects the previous calendar day with
`date -v-1d +%F`, because same-day ASC sales insights can report `unavailable`.
Loading or unloading a scheduler is a manual deployment action, not part of public-core setup:

```bash
mkdir -p "${HOME}/Library/LaunchAgents"
launchctl setenv COMPOUND_REPO "$(pwd)"
launchctl setenv COMPOUND_ASC_PRODUCTS "${COMPOUND_HOME:-${HOME}/.compound}/private/asc-products.json"
launchctl setenv COMPOUND_ASC_DAILY_COMMAND 'asc insights daily --app {app_id} --vendor {vendor} --date {date} --output json'
cp tools/templates/ai.compound.asc-daily.plist "${HOME}/Library/LaunchAgents/ai.compound.asc-daily.plist"
launchctl bootstrap "gui/$(id -u)" "${HOME}/Library/LaunchAgents/ai.compound.asc-daily.plist"
launchctl bootout "gui/$(id -u)" "${HOME}/Library/LaunchAgents/ai.compound.asc-daily.plist"
```
