# ASC Daily Signals

`tools/asc_daily_signals.ts` is a rented edge. It reads operator-owned App Store Connect output, writes public-core `compound.product_signal_export.v0.1` files under `COMPOUND_HOME`, and applies them through the existing `compound product signal` door.

Private app config belongs under `COMPOUND_HOME` or `docs_private/`, never tracked. JSON or minimal YAML `apps:` list is accepted. Minimal JSON shape:

```json
{
  "apps": [
    {
      "product_id": "product:example",
      "asc_app_id": "123456789",
      "surface": "app-store-page",
      "vendor": "12345678",
      "metrics": ["units", "developer_proceeds_usd", "impressions", "product_page_views", "product_page_conversion_rate"]
    }
  ]
}
```

Dry run against a saved ASC snapshot:

```bash
bun tools/asc_daily_signals.ts --config "${COMPOUND_HOME:-${HOME}/.compound}/private/asc-products.json" --asc-json fixtures/product/asc-daily-signals.json --json
```

## Sales-series JSONL bridge

If another job already writes App Store Connect sales rows as JSONL, Compound should not fetch that source a second time. Use the bridge to map an operator-owned product config to the existing `compound product signal --asc-sales-series` adapter:

```bash
bun tools/asc_sales_series_bridge.ts \
  --config "${COMPOUND_HOME:-${HOME}/.compound}/private/asc-products.json" \
  --sales-series "${ASC_SALES_SERIES_JSONL}" \
  --change-event "Describe the product/app-store change being evaluated." \
  --before 2026-07-01/2026-07-07 \
  --after 2026-07-08/2026-07-14 \
  --metric units,developer_proceeds_usd \
  --json
```

Add `--apply` only when the intended target is the current Compound runtime. For tests or rehearsals, set `COMPOUND_HOME` to a temp directory and pass a temp-built `--compound-bin`. The bridge does not call ASC, does not load launchd, and does not create a metrics store; it only plans or invokes the existing product-signal door.

Live run with the operator's ASC command template:

```bash
COMPOUND_ASC_DAILY_COMMAND='asc insights daily --app {app_id} --vendor {vendor} --date {date} --output json' bun tools/asc_daily_signals.ts --config "${COMPOUND_HOME:-${HOME}/.compound}/private/asc-products.json" --asc-command "${COMPOUND_ASC_DAILY_COMMAND}" --apply --json
```

Launchd is operator-loaded only:

```bash
mkdir -p "${HOME}/Library/LaunchAgents"
launchctl setenv COMPOUND_REPO "$(pwd)"
launchctl setenv COMPOUND_ASC_PRODUCTS "${COMPOUND_HOME:-${HOME}/.compound}/private/asc-products.json"
launchctl setenv COMPOUND_ASC_DAILY_COMMAND 'asc insights daily --app {app_id} --vendor {vendor} --date {date} --output json'
cp ops/launchd/ai.compound.asc-daily.plist "${HOME}/Library/LaunchAgents/ai.compound.asc-daily.plist"
launchctl bootstrap "gui/$(id -u)" "${HOME}/Library/LaunchAgents/ai.compound.asc-daily.plist"
launchctl bootout "gui/$(id -u)" "${HOME}/Library/LaunchAgents/ai.compound.asc-daily.plist"
```
