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
