// App page cases: the pure models and server-rendered blocks of one app's
// page (/apps/<slug>), on synthetic data with an injected clock. Imported by
// scripts/growth-cases.mjs, which the wired console-ads-smoke runs, so a
// failing case fails that gate. Alone: bun scripts/app-page-cases.mjs
import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";

import { buildWorkProjectIndex } from "../src/lib/work-project.ts";
import { appWorkModel } from "../src/components/growth/app-work.ts";
import { AppWork, APP_WORK_SHOWN } from "../src/components/growth/AppWork.tsx";
import { AppAds, AppNumbers, funnelSubtitle } from "../src/components/growth/AppNumbers.tsx";
import { AppNeedsYou } from "../src/components/growth/AppNeedsYou.tsx";
import { readFileSync } from "node:fs";
import { KpiTile } from "../src/components/growth/GrowthKit.tsx";
import { AdsPlatformCard } from "../src/components/ads/AdsPlatformCard.tsx";
import { ADS_GOOD_DIRECTION, campaignStateLabel, hasPlaceholder, mapAdsProjection, mapTotals } from "../src/lib/ads.ts";
import { AdsCampaignTable } from "../src/components/ads/AdsTables.tsx";
import { DeltaPill } from "../src/components/kit/DeltaPill.tsx";
import { allocationWhy, funnelPeriods, kpiPeriod, mapGrowthModel } from "../src/lib/growth.ts";

const NOW = Date.parse("2030-03-20T12:00:00Z");
const DAY = 86_400_000;
const ns = (daysAgo) => String(BigInt(NOW - daysAgo * DAY) * 1000000n);
const text = (html) => html.replace(/<[^>]+>/g, " ").replace(/\s+/g, " ").trim();

/* ---------- 1. All of an app's work, on hold and not moved first ---------- */
{
  // One app recorded under two spellings and a code folder; the old page
  // matched only `canonical_surface === "product:pocket-log"` exactly.
  const catalog = [
    { productId: "product:pocket-log", name: "Pocket Log", aliases: ["product:pocket_log"] },
    { productId: "product:tide-chart", name: "Tide Chart", aliases: [] },
  ];
  const inventory = [{ id: "product:pocket_log", codeIn: ["apps/pocket-log"] }];
  const open = [
    { work_id: "work_pl_exact", state: "verifying", claimed_at: ns(1), success_predicate: "Exact id item", canonical_surface: "product:pocket-log" },
    { work_id: "work_pl_alias", state: "running", claimed_at: ns(12), success_predicate: "Alias spelling item", canonical_surface: "product:pocket_log" },
    { work_id: "work_pl_case", state: "claimed", claimed_at: ns(20), success_predicate: "Upper case item", canonical_surface: "product:Pocket-Log" },
    { work_id: "work_pl_folder", state: "held", claimed_at: ns(3), success_predicate: "Folder item on hold", target_worktree_root: "/srv/example/apps/pocket-log" },
    { work_id: "work_pl_scope", state: "verifying", claimed_at: ns(2), desired_outcome: "Recorded product item", scope: { product_id: "pocket_log" } },
    { work_id: "work_tc_other", state: "running", claimed_at: ns(1), success_predicate: "Another app", canonical_surface: "product:tide-chart" },
  ];
  const index = buildWorkProjectIndex(open, catalog, inventory, NOW);
  const model = appWorkModel(open, index, "product:pocket-log", NOW);
  assert.ok(model, "a built index gives a model");
  assert.deepEqual(
    model.rows.map((row) => row.id),
    ["work_pl_folder", "work_pl_case", "work_pl_alias", "work_pl_exact", "work_pl_scope"],
    "every spelling of the app is its work; on hold first, then not moved (oldest first), then the rest (newest first)",
  );
  assert.deepEqual(model.counts, { open: 5, needYou: 1, inProgress: 4, waitingForCheck: 2, stale: 2 }, "counts are the index's");
  assert.equal(model.rows[0].onHold, true);
  assert.equal(model.rows[1].stale, true);
  assert.equal(model.rows[1].days, 20);
  assert.equal(model.rows[3].state, "Waiting for a proof check", "states in plain words");
  assert.equal(model.rows[0].href, "/work/work_pl_folder");
  assert.equal(model.hrefs.all, "/work?lens=table&filter=all&app=pocket-log");
  assert.equal(model.hrefs.onHold, "/work?lens=table&filter=blocked&app=pocket-log");
  assert.equal(model.hrefs.stale, "/work?lens=table&filter=stale&app=pocket-log");

  const html = renderToStaticMarkup(createElement(AppWork, { name: "Pocket Log", model, missing: [], reads: [] }));
  const words = text(html);
  assert.ok(words.includes("5 open work items"), words);
  assert.ok(words.includes("1 on hold, waiting for you"), words);
  assert.ok(words.includes("2 not moved in a week"), words);
  assert.ok(words.indexOf("Folder item on hold") < words.indexOf("Exact id item"), "on hold leads the list");
  assert.ok(html.includes('href="/work?lens=table&amp;filter=all&amp;app=pocket-log"'), "the count opens the app's work in Work");

  // More items than the page lists: a "See all N in Work" link, never a silent cut.
  const many = Array.from({ length: APP_WORK_SHOWN + 3 }, (_, i) => ({
    work_id: `work_pl_many_${i}`, state: "running", claimed_at: ns(1), success_predicate: `Item ${i}`, canonical_surface: "product:pocket-log",
  }));
  const big = appWorkModel(many, buildWorkProjectIndex(many, catalog, inventory, NOW), "product:pocket-log", NOW);
  const bigWords = text(renderToStaticMarkup(createElement(AppWork, { name: "Pocket Log", model: big, missing: [], reads: [] })));
  assert.ok(bigWords.includes(`See all ${APP_WORK_SHOWN + 3} in Work`), bigWords);

  // An app with no open work says so; the index not built yet is unknown, never "no work".
  const none = appWorkModel(open, index, "product:tide-chart-missing", NOW);
  assert.equal(none.counts.open, 0);
  assert.ok(text(renderToStaticMarkup(createElement(AppWork, { name: "Quiet App", model: none, missing: [], reads: [] }))).includes("No open work for Quiet App."));
  assert.equal(appWorkModel(open, null, "product:pocket-log", NOW), null);
  const loading = {
    status: "loading", generatedAt: null, lastGoodAt: null, error: null, failedAt: null, updating: true, outOfDate: false,
    warn: false, ageMs: null, lastRunMs: null, source: "compound workbench --json",
  };
  const pending = text(renderToStaticMarkup(createElement(AppWork, { name: "Pocket Log", model: null, missing: [{ label: "open work", read: loading }], reads: [] })));
  assert.ok(pending.includes("Reading open work for the first time"), pending);
  assert.ok(!/No open work|\b0 open\b/.test(pending), "a read that has not landed is never zero");
}

console.log("app page cases ok: all of an app's work under every spelling, on hold first, see-all link, unknown never zero");

/* ---------- 2. Numbers agree and name their days ---------- */
{
  const kpi = (value, extra = {}) => ({ value, previous: value, has_data: true, latest_date: "2030-03-13", days_reported: 30, days_in_window: 30, unit: "usd", ...extra });
  const windowed = { window_start: "2030-02-12", window_end: "2030-03-13" };
  const step = (id, label, value, extra = {}) => ({ id, label, value, as_of: "2030-03-14", status: value == null ? "blind" : "measured", note: "", ...extra });
  const appPayload = (extra) => ({
    product_id: "product:pocket-log", name: "Pocket Log", aliases: [],
    freshness: { latest_date: "2030-03-14", stale: false, sources: [{ source: "app_store_connect", latest_date: "2030-03-14", days_since: 2, stale: false }, { source: "apple_search_ads", latest_date: "2030-03-16", days_since: 0, stale: false }] },
    series: { proceeds_usd: [{ date: "2030-03-12", value: 4 }, { date: "2030-03-13", value: 5 }], ad_spend_usd: [{ date: "2030-03-13", value: 2 }] },
    ...extra,
  });
  const model = (apps, allocation = []) =>
    mapGrowthModel({ schema_version: "compound.product_growth.v0.2", apps, portfolio: { allocation } });

  // Older CLI: no window fields. Its ranking sentence summed other days than the tile.
  const old = model(
    [appPayload({
      kpis: { proceeds_usd: kpi(1120.51), ad_spend_usd: kpi(155.5, { latest_date: "2030-03-16" }), ad_conversions: kpi(156, { unit: "count", latest_date: "2030-03-16" }), ad_cpa_usd: kpi(1, { latest_date: "2030-03-16" }) },
      funnel: [step("impressions", "App Store impressions", 327), step("product_page_views", "Product page views", 13), step("first_time_downloads", "First-time downloads", 533, { as_of: "2030-03-13" }), step("activation", "Activation", null)],
    })],
    [{ product_id: "product:pocket-log", name: "Pocket Log", rank: 1, focus: "grow", reason: "$1,035.77 proceeds in 30 days ($3,410.70 in 90 days) with sales data fresh through 2030-03-14.", rule: "" }],
  );
  const oldApp = old.apps[0];
  assert.equal(kpiPeriod(oldApp.kpis.proceeds_usd), "last 30 days · newest data Mar 13", "an older CLI's tile says its newest day, never a guessed range");
  const why = allocationWhy(old.portfolio.allocation[0], oldApp);
  assert.ok(!why.includes("1,035"), `the ranking row never quotes a second 30-day proceeds figure: ${why}`);
  assert.ok(why.startsWith("$1,121 proceeds, last 30 days"), why);
  // Its funnel steps do not say their days: never drawn as one funnel.
  assert.equal(funnelPeriods(oldApp.funnel).undated, true);
  assert.ok(funnelSubtitle(oldApp.funnel).includes("not as a funnel"), funnelSubtitle(oldApp.funnel));
  const oldHtml = renderToStaticMarkup(createElement(AppNumbers, { app: oldApp }));
  assert.ok(!oldHtml.includes("→</span></li>") && !/aria-hidden="true">→/.test(oldHtml), "no arrows between undated steps");
  assert.ok(!oldHtml.includes("1,035"), "the app page shows one proceeds figure");

  // Newer CLI: window fields on tiles, the ranking row and the funnel.
  const fresh = model(
    [appPayload({
      kpis: { proceeds_usd: kpi(1120.51, windowed), ad_spend_usd: kpi(155.5, { window_start: "2030-02-15", window_end: "2030-03-16" }), ad_conversions: kpi(156, { unit: "count" }), ad_cpa_usd: kpi(1) },
      funnel: [
        step("impressions", "App Store impressions", 14628, { ...windowed, days_reported: 29 }),
        step("first_time_downloads", "New downloads", 533, windowed),
        step("purchases", "Purchases", 9, { ...windowed, status: "partial", days_reported: 12 }),
        step("activation", "Activation", 0.4),
        step("renewals", "Renewals", null),
      ],
    })],
    [{ product_id: "product:pocket-log", name: "Pocket Log", rank: 1, focus: "grow", reason: "$1,120.51 proceeds in 30 days, 2030-02-12 to 2030-03-13.", rule: "", ...windowed }],
  );
  const app = fresh.apps[0];
  assert.equal(kpiPeriod(app.kpis.proceeds_usd), "Feb 12 – Mar 13");
  assert.equal(allocationWhy(fresh.portfolio.allocation[0], app), "$1,120.51 proceeds in 30 days, 2030-02-12 to 2030-03-13.", "a dated reason is shown as written");
  const periods = funnelPeriods(app.funnel);
  assert.equal(periods.shared, "Feb 12 – Mar 13");
  assert.deepEqual(periods.steps, ["", "", "", "one reading, Mar 14", ""]);
  assert.ok(funnelSubtitle(app.funnel).includes("Feb 12 – Mar 13"));
  const tile = text(renderToStaticMarkup(createElement(KpiTile, { kpiKey: "proceeds_usd", kpi: app.kpis.proceeds_usd, color: "blue" })));
  assert.ok(tile.includes("Proceeds Feb 12 – Mar 13"), tile);
  assert.ok(tile.includes("vs. the 30 days before"), tile);
  const html = renderToStaticMarkup(createElement(AppNumbers, { app }));
  const words = text(html);
  const adsHtml = renderToStaticMarkup(createElement(AppAds, { app }));
  const adsWords = text(adsHtml);
  assert.ok(adsWords.includes("Ad spend Feb 15 – Mar 16") && adsWords.includes("Ad installs") && adsWords.includes("Cost per install"), adsWords);
  assert.ok(adsHtml.includes('href="/ads?product=product%3Apocket-log&amp;window=30"'), "the ad tiles open this app's Ads page");
  assert.ok(words.includes("at least"), "a partial funnel step reads as a lower bound");
  assert.ok(/aria-hidden="true">→/.test(html), "dated steps are drawn as one funnel");
  assert.ok(words.includes("New downloads"), "the downloads tile and the funnel use one name");
  assert.ok(!/\bCPA\b/.test(words + adsWords), "one name for cost per install");

  // The Ads page's platform card uses the same name.
  const card = text(renderToStaticMarkup(createElement(AdsPlatformCard, {
    windowDays: 30,
    view: {
      platform: "apple_search_ads", label: "Apple Search Ads", status: "connected", exportHint: "", firstDate: "2030-02-01", lastDate: "2030-03-16",
      lastImportAt: "", daysSinceLastDate: 0, totals: mapTotals({ spend: 10, conversions: 5, cpa: 2, currency: "USD", rows: 3 }), previous: mapTotals({}),
      daily: [], products: [], campaignsActive: 1, campaignsTotal: 1, nextMove: { text: "", command: "" },
    },
  })));
  assert.ok(card.includes("Per install") && !/\bCPA\b/.test(card), card);
}

console.log("app page cases ok: numbers name their days, one proceeds figure, funnel never mixes undated steps, one name for cost per install");

/* ---------- 3. The page reads as a briefing, in the owner's order ---------- */
{
  const source = readFileSync(new URL("../src/app/apps/[slug]/page.tsx", import.meta.url), "utf8");
  const body = source.slice(source.indexOf("return (\n    <div className=\"flex flex-col gap-8\">"));
  const order = ["<AppNeedsYou", "<AppNumbers", "<AppWork", 'id="app-next-steps"', "<AppAds", "<ProfileRow", "<GroupAudit"];
  const at = order.map((mark) => body.indexOf(mark));
  assert.ok(at.every((i) => i >= 0), `every block is on the page: ${order.filter((_, i) => at[i] < 0)}`);
  assert.deepEqual([...at].sort((a, b) => a - b), at, "needs you, numbers, work, next steps, ads, then the profile and groups");
  assert.ok(body.includes("<BreadcrumbTitle title={app.name} />"), "the tab and breadcrumb name the app");
  assert.ok(/action=\{null\}/.test(body), "no header button that only scrolls");
  assert.ok(body.includes("lead={`How ${app.name} is doing"), "the app page has its own purpose line, not the overview's");

  // Needs you: held work first; unknown while reading; "nothing" only when known.
  const work = { rows: [
    { id: "work_a", title: "Held fixture item", state: "On hold", onHold: true, stale: false, days: 2, href: "/work/work_a" },
    { id: "work_b", title: "Running fixture item", state: "Running", onHold: false, stale: false, days: 1, href: "/work/work_b" },
  ], counts: { open: 2, needYou: 1, inProgress: 1, waitingForCheck: 0, stale: 0 }, hrefs: { all: "/work", onHold: "/work?x", stale: "/work?y", checking: "/work?z" } };
  const needs = text(renderToStaticMarkup(createElement(AppNeedsYou, { name: "Pocket Log", work, verdicts: [{ text: "Did the new subtitle help?", href: "/reviews?decision=k" }] })));
  assert.ok(needs.includes("On hold, waiting for you Held fixture item") && needs.includes("Verdict due Did the new subtitle help?"), needs);
  assert.ok(!needs.includes("Running fixture item"), "only what waits for him");
  assert.ok(text(renderToStaticMarkup(createElement(AppNeedsYou, { name: "Pocket Log", work: null, verdicts: [] }))).includes("not yet known"));
  assert.ok(text(renderToStaticMarkup(createElement(AppNeedsYou, { name: "Pocket Log", work: { ...work, rows: [] }, verdicts: [] }))).includes("Nothing about Pocket Log waits for you"));

  // No ads: one quiet line, not an empty block.
  const quiet = mapGrowthModel({ schema_version: "compound.product_growth.v0.2", apps: [{ product_id: "product:quiet-app", name: "Quiet App" }] }).apps[0];
  assert.equal(text(renderToStaticMarkup(createElement(AppAds, { app: quiet }))), "Ads: none run for this app. No ad report names it.");
}

console.log("app page cases ok: the page leads with what needs you, then numbers, work, next steps and ads");

/* ---------- 4. Ads: colours follow meaning, quiet campaigns say so, no placeholder commands ---------- */
{
  const day = (date, spend, impressions) => ({ date, currency: "USD", spend, impressions, clicks: 0, conversions: 0 });
  const daily = (lastActive) => ["2030-03-08", "2030-03-09", "2030-03-10", "2030-03-11", "2030-03-12", "2030-03-13", "2030-03-14", "2030-03-15", "2030-03-16"]
    .map((d) => day(d, d <= lastActive ? 1.5 : 0, d <= lastActive ? 9 : 0));
  const campaign = (id, name, lastActive, extra = {}) => ({
    platform: "apple_search_ads", product_id: "product:pocket-log", account: "acct-1", campaign_id: id, campaign_name: name, status: "active",
    first_date: "2030-03-08", last_date: "2030-03-16", totals: { spend: 3, currency: "USD", rows: 9 }, previous: {}, daily: daily(lastActive), ...extra,
  });
  const payload = (campaigns) => ({
    schema_version: "compound.product_ads.v0.1", status: "ok", as_of: "2030-03-16",
    window: { days: 30, start: "2030-02-15", end: "2030-03-16" },
    counts: { rows: 18, rows_in_window: 18, campaigns: campaigns.length, campaigns_active: campaigns.length },
    platforms: [{ platform: "apple_search_ads", label: "Apple Search Ads", status: "connected", campaigns_active: campaigns.length, campaigns_total: campaigns.length }],
    campaigns,
  });

  // An older CLI: every campaign with a recent row is "active", even with $0 spend for ten days.
  const old = mapAdsProjection(payload([campaign("c1", "Pocket Log Region One — Search Results", "2030-03-16"), campaign("c2", "Pocket Log Region Two — Search Results", "2030-03-06")]));
  assert.equal(old.campaigns[0].status, "active");
  assert.equal(old.campaigns[1].status, "quiet", "no spend or impressions in the last 3 days is quiet, not active");
  assert.equal(old.campaigns[1].lastActiveDate, "", "no active day inside the window");
  assert.equal(campaignStateLabel(old.campaigns[1]), "quiet, no spend in this window");
  assert.equal(old.counts.campaignsActive, 1, "the active count follows the campaigns");
  assert.equal(old.platforms[0].campaignsActive, 1);
  const older = mapAdsProjection(payload([campaign("c3", "Pocket Log Region Three", "2030-03-10")]));
  assert.equal(campaignStateLabel(older.campaigns[0]), "quiet since Mar 10");
  // A newer CLI sends last_active_date and its own status: trusted as is.
  const fresh = mapAdsProjection(payload([campaign("c4", "Pocket Log Region Four", "2030-03-16", { status: "quiet", last_active_date: "2030-03-06" })]));
  assert.equal(fresh.campaigns[0].status, "quiet");
  assert.equal(campaignStateLabel(fresh.campaigns[0]), "quiet since Mar 6");

  const table = renderToStaticMarkup(createElement(AdsCampaignTable, { campaigns: old.campaigns, windowDays: 30 }));
  assert.ok(text(table).includes("Pocket Log Region Two — Search Results"), "full campaign names");
  assert.ok(!table.includes(">c1<") && !table.includes("acct-1 <"), "campaign and account ids stay out of the visible text");
  assert.ok(table.includes('href="/apps/pocket-log"') && text(table).includes("Pocket Log"), "the app links to its page by name");

  // Good and bad follow the number's meaning: fewer installs is bad, less spend is good.
  assert.equal(ADS_GOOD_DIRECTION.conversions, "up");
  assert.equal(ADS_GOOD_DIRECTION.impressions, "up");
  assert.equal(ADS_GOOD_DIRECTION.clicks, "up");
  assert.equal(ADS_GOOD_DIRECTION.spend, "down");
  const pill = renderToStaticMarkup(createElement(DeltaPill, { value: -24, suffix: "%", goodDirection: ADS_GOOD_DIRECTION.conversions }));
  assert.ok(pill.includes("text-crit") && !pill.includes("text-good"), "falling installs are red");
  const adsPage = readFileSync(new URL("../src/app/ads/page.tsx", import.meta.url), "utf8");
  assert.ok(!/label="(Impressions|Clicks|Installs)"[^\n]*goodDirection="down"/.test(adsPage) && !adsPage.includes('goodDirection="down" title'), "no tile hard-codes down as good");
  for (const label of ["Impressions", "Clicks", "Installs"]) {
    assert.ok(new RegExp(`label="${label}"[^\\n]*goodDirection=\\{ADS_GOOD_DIRECTION\\.`).test(adsPage), `${label} tile follows its meaning`);
  }
  // Campaigns come before platforms, and platforms without a report fold into one line.
  assert.ok(adsPage.indexOf('id="campaign-results" tabIndex={-1} className="scroll-mt-6 flex') < adsPage.indexOf("Platforms with reports"), "campaign table first");
  assert.ok(adsPage.includes("<details") && adsPage.includes("platforms have\"} no report"), "unconnected platforms fold into one line");

  // A command with a placeholder never reaches the owner.
  assert.equal(hasPlaceholder("compound product ads import --platform x --product <product:id> --report <export.csv>"), true);
  assert.equal(hasPlaceholder("compound product ads --json"), false);
  const waitingCard = text(renderToStaticMarkup(createElement(AdsPlatformCard, {
    windowDays: 30,
    view: {
      platform: "meta_ads", label: "Meta Ads", status: "stale", exportHint: "", firstDate: "", lastDate: "2030-01-01", lastImportAt: "", daysSinceLastDate: 70,
      totals: mapTotals({}), previous: mapTotals({}), daily: [], products: [], campaignsActive: 0, campaignsTotal: 0,
      nextMove: { text: "Stale. Import a newer report.", command: "compound product ads import --platform meta_ads --product <product:id> --report <export.csv>" },
    },
  })));
  assert.ok(waitingCard.includes("Stale. Import a newer report.") && !waitingCard.includes("<product:id>") && !waitingCard.includes("compound product ads import"), waitingCard);
}

console.log("app page cases ok: ads colours follow meaning, quiet campaigns say since when, campaigns first, no placeholder commands");
