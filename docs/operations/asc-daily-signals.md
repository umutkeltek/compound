# ASC Daily Signals

Compound does not own App Store Connect fetching. ASC ingestion is a rented
edge: any collector can produce ASC-shaped output, and Compound only normalizes
that output into product signals through existing product-loop doors.

Keep private app IDs, vendor IDs, credentials, and account-specific mappings out
of tracked public files. Put them under `COMPOUND_HOME` or another private
runtime location. JSON or minimal YAML `apps:` lists are accepted.

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
  --change-event "Describe product or App Store change being evaluated." \
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

## Scheduling

Scheduling is deployment-specific. Use cron, systemd timers, launchd, GitHub
Actions, a hosted worker, or any other scheduler that can run the same command
with the private config and ASC-producing command available.

The bundled launchd plist is only a macOS example. Loading or unloading it is a
manual deployment action, not part of public-core setup:

```bash
mkdir -p "${HOME}/Library/LaunchAgents"
launchctl setenv COMPOUND_REPO "$(pwd)"
launchctl setenv COMPOUND_ASC_PRODUCTS "${COMPOUND_HOME:-${HOME}/.compound}/private/asc-products.json"
launchctl setenv COMPOUND_ASC_DAILY_COMMAND 'asc insights daily --app {app_id} --vendor {vendor} --date {date} --output json'
cp ops/launchd/ai.compound.asc-daily.plist "${HOME}/Library/LaunchAgents/ai.compound.asc-daily.plist"
launchctl bootstrap "gui/$(id -u)" "${HOME}/Library/LaunchAgents/ai.compound.asc-daily.plist"
launchctl bootout "gui/$(id -u)" "${HOME}/Library/LaunchAgents/ai.compound.asc-daily.plist"
```
