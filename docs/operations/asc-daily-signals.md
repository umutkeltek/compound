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
cp ops/launchd/ai.compound.asc-daily.plist "${HOME}/Library/LaunchAgents/ai.compound.asc-daily.plist"
launchctl bootstrap "gui/$(id -u)" "${HOME}/Library/LaunchAgents/ai.compound.asc-daily.plist"
launchctl bootout "gui/$(id -u)" "${HOME}/Library/LaunchAgents/ai.compound.asc-daily.plist"
```
