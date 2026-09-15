# Ads panel — paid acquisition on every platform

Compound does not own ad-platform fetching. Like App Store Connect, Search
Console, GA4 and AdSense, each ad platform is a rented edge: **you export the
platform's own daily campaign report, Compound normalizes it into one ledger,
and every reader projects from that ledger.** No credentials, no API calls, no
second fetcher.

Keep account ids, exported reports and product mappings out of tracked public
files. Reports live wherever you download them; the ledger lives under
`COMPOUND_HOME/ledger/`.

## Where it shows up

| Surface | What it reads |
|---|---|
| Console → **Outcomes → Ads** (`/ads`) | `compound product ads --json` |
| Console → Outcomes → Feedback loops | bridged `ads_*` ProductSignals |
| Console → Knowledge → Sources | each platform as a source connection (not connected / observed) |
| `compound product ads` | the same projection as markdown |

## Platforms

`compound product ads platforms` lists the registry and the exact report
headers each dialect understands:

| id | Export |
|---|---|
| `apple_search_ads` | Apple Search Ads → Campaigns → date range → Export (CSV), campaign level, daily |
| `google_ads` | Google Ads → Campaigns → segment by Day → Download CSV (title lines + Total rows are skipped) |
| `meta_ads` | Ads Manager → Campaigns → Breakdown by Day → Export table data (CSV) |
| `tiktok_ads` | TikTok Ads Manager → Campaign → custom report by day → Export CSV (not XLSX) |
| `x_ads` | X Ads → Campaigns → date range → Export, campaign, daily |
| `reddit_ads` | Reddit Ads → Reporting → group by Day → Export |
| `linkedin_ads` | Campaign Manager → Export → campaign performance, daily |
| `other` | Any platform, with canonical headers (below) |

Every platform also accepts the canonical headers, so an export the adapter
does not recognise can be hand-mapped without code:

```
date, campaign_id, campaign_name, spend, impressions, clicks, conversions, revenue, currency
```

Required: `date`, `spend`, `impressions`. Missing `clicks` / `conversions` /
`revenue` make CTR / CPA / ROAS read as undefined ("—") rather than zero.
Rows whose date cell is not a date (a `Total` line) are skipped and counted.
Slash dates are read as US month/day; dotted dates as day.month. Numbers may
carry a currency symbol, thousands separators, or a decimal comma
(`1.250,40`).

## Import

```bash
compound product ads import \
  --platform apple_search_ads \
  --product product:demo-ios \
  --report ~/Downloads/searchads-campaigns-2026-09.csv
# optional: --account <label>  (one product on several accounts of one platform)
#           --currency USD     (when the export carries no currency column)
#           --json
```

- Identity is `(platform, product, account, campaign_id, date)`. Report rows
  sharing that identity (an export split by device or country) are summed.
- The ledger is append-only. Re-importing the same report writes no new row
  lines (only an import receipt). A changed value appends a newer observation;
  the projection folds to the latest one per identity.
- A report the adapter cannot parse is refused; nothing is written.

Ledger files: `ledger/product-ads.jsonl` (rows) and
`ledger/product-ads-imports.jsonl` (receipts with the report's sha256).

## Read

```bash
compound product ads                       # markdown, last 30 days
compound product ads --json --window 7d    # what the console renders
compound product ads --product product:demo-app --platform meta_ads --as-of 2026-09-03
```

Per platform: `connected` (newest row within 72 h of as-of), `stale` (older),
or `not_connected` (no rows) — the whole registry is always rendered, with the
exact next command. Totals carry CTR / CPC / CPA / CPM / ROAS with zero
denominators reported as 0 and readers rendering "—"; rows in more than one
currency are flagged `mixed_currency` with `spend_by_currency` instead of a
summed number.

## Feed the loop

An ads window becomes product feedback through the same door as every other
signal:

```bash
compound product ads signal \
  --platform apple_search_ads --product product:demo-ios \
  --before 2026-08-25/2026-08-31 --after 2026-09-01/2026-09-07 \
  --metric spend,conversions,cpa \
  --change-event "New product page screenshots shipped 2026-09-01"
```

This records `ads_spend`, `ads_conversions`, `ads_cpa` ProductSignals
(`Source.Kind` = the platform) through `ApplySignal`, so they appear in the
loop, can be matched to a hypothesis, and reviewed with a verdict. Metrics
whose denominator is missing in both windows are skipped, never recorded as
zero. Replaying the same command is idempotent.

## Proof

```bash
go test ./internal/product -run 'Ads|ParseAdNumber'
go test ./internal/observation -run Ads
cd console && bun scripts/ads-smoke.mjs
```

Fixtures for every dialect live under `fixtures/product/ads/`.
