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
import { ProductLoopCard } from "../src/components/products/ProductLoopCard.tsx";
import { foldLoopsByApp, metricLabel, verdictDue } from "../src/lib/products.ts";
import { KpiTile } from "../src/components/growth/GrowthKit.tsx";
import { AdsPlatformCard } from "../src/components/ads/AdsPlatformCard.tsx";
import { ADS_GOOD_DIRECTION, campaignStateLabel, hasPlaceholder, mapAdsProjection, mapTotals } from "../src/lib/ads.ts";
import { AdsCampaignTable } from "../src/components/ads/AdsTables.tsx";
import { DeltaPill } from "../src/components/kit/DeltaPill.tsx";
import { allocationWhy, funnelPeriods, kpiPeriod, mapGrowthModel, proceeds90Window, sumBetween } from "../src/lib/growth.ts";

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
  // One 90-day window for proceeds on the portfolio page: the ranking's "in 90 days" ends on the proceeds window's
  // last day, not on the series' last day; the panel sums those same days and both say the dates.
  assert.deepEqual(proceeds90Window("2030-03-13"), { start: "2029-12-14", end: "2030-03-13" });
  assert.equal(proceeds90Window(""), null);
  assert.equal(sumBetween([{ date: "2029-12-13", value: 7 }, { date: "2029-12-14", value: 1 }, { date: "2030-03-13", value: 2 }, { date: "2030-03-14", value: 9 }, { date: "2030-03-15", value: null }], "2029-12-14", "2030-03-13"), 3);
  const dated90 = { product_id: "product:pocket-log", name: "Pocket Log", rank: 1, focus: "grow", rule: "", ...windowed,
    reason: "$1,120.51 proceeds in 30 days, 2030-02-12 to 2030-03-13 ($3,410.70 in 90 days), with sales data fresh through 2030-03-14." };
  assert.equal(allocationWhy(model([], [dated90]).portfolio.allocation[0], app),
    "$1,120.51 proceeds in 30 days, 2030-02-12 to 2030-03-13 ($3,410.70 in 90 days, Dec 14 – Mar 13), with sales data fresh through 2030-03-14.",
    "the ranking's 90-day figure carries the dates the panel uses");
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
  const order = ["<AppNeedsYou", "<AppNumbers", "<AppWork", 'id="app-next-steps"', "<AppResults", "<AppAds", "<AppWhere", "<ProfileRow", "<GroupAudit"];
  const at = order.map((mark) => body.indexOf(mark));
  assert.ok(at.every((i) => i >= 0), `every block is on the page: ${order.filter((_, i) => at[i] < 0)}`);
  assert.deepEqual([...at].sort((a, b) => a - b), at, "needs you, numbers, work, next steps, ads, then the profile and groups");
  assert.ok(body.includes("<BreadcrumbTitle title={app.name} />"), "the tab and breadcrumb name the app");
  // One total of areas: the header's "N of M areas measured" uses the same total as "All G groups, M areas".
  assert.ok(source.includes("const areasTotal = model.areas.length;") && body.includes("`${app.coverage.measured} of ${areasTotal}`") && body.includes("{model.areas.length} areas"),
    "the header and the groups heading name one total of areas");
  // No rank by proceeds without proceeds to rank by.
  assert.ok(body.includes("{rankedByProceeds ? (") && !/#\{allocation\.rank\}/.test(body), "a rank by proceeds is shown only with a proceeds figure");
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

/* ---------- 5. Results: one card per app, a real verdict links its decision ---------- */
{
  const loop = (productId, extra = {}) => ({
    productId, name: productId, aliases: [], hypotheses: [], signals: [], metricNames: [], hypothesisCount: 0, reviews: [],
    sourceIssueCount: 0, freshness: { latestSignalAt: "", latestReviewAt: "", stale: false }, loopGaps: [], signalCount: 0, ...extra,
  });
  const signal = (metric, after, observedAt) => ({ signalId: `${metric}-${observedAt}`, provider: "app_store_connect", metric, before: null, after, observedAt, changeEvent: "", transitionRef: "" });
  const draft = { id: "hyp_d", statement: "Daily metric snapshot observed.", expectedEffect: "OPERATOR MUST WRITE: what should it do?", metrics: [], window: "2030-01-01..2030-01-10", placeholder: true };
  const real = { id: "hyp_r", statement: "A shorter subtitle lifts page views", expectedEffect: "Product page views rise 10%", metrics: ["app.impressions"], window: "2030-02-01..2030-02-28", placeholder: false };
  const catalog = [
    { productId: "product:pocket-log", name: "Pocket Log", aliases: ["product:pocket_log", "product:pocket_log_web"] },
    { productId: "product:tide-chart", name: "Tide Chart", aliases: [] },
    { productId: "product:never-measured", name: "Never Measured", aliases: [] },
  ];
  const rows = [
    loop("product:pocket-log", { signals: [signal("app_store_connect_developer_proceeds_usd", 21.24, "2030-03-01T00:00:00Z")], signalCount: 1, hypotheses: [draft], loopGaps: ["review_ready"] }),
    loop("product:pocket_log_web"),
    loop("product:Pocket_Log"),
    loop("product:tide-chart", { signals: [signal("app.impressions", 900, "2030-03-02T00:00:00Z")], signalCount: 1, hypotheses: [real], loopGaps: ["review_ready"] }),
    loop("product:orphan_thing"),
  ];
  const { loops, missing } = foldLoopsByApp(rows, catalog);
  assert.deepEqual(loops.map((l) => [l.productId, l.name]), [["product:pocket-log", "Pocket Log"], ["product:tide-chart", "Tide Chart"], ["product:orphan_thing", "Orphan Thing"]], "every spelling of an app is one card, named by the catalog");
  assert.deepEqual(missing.map((a) => a.name), ["Never Measured"], "apps with no record are named, not dropped");
  const pocket = loops[0];
  assert.equal(verdictDue(pocket), false, "a draft nobody wrote is not a verdict waiting on him");
  assert.equal(verdictDue(loops[1]), true);

  const draftCard = renderToStaticMarkup(createElement(ProductLoopCard, { loop: pocket }));
  const draftWords = text(draftCard);
  assert.ok(!draftWords.includes("verdict due") && draftWords.includes("Nothing waits for you"), draftWords);
  assert.ok(!draftCard.includes('href="/reviews"'), "never the general Decide list");
  assert.ok(draftWords.includes("Developer proceeds in US dollars") && draftWords.includes("Mar 1, 2030"), "readings in words, with their date");
  assert.equal(metricLabel("app_store_connect_units"), "Units");

  // A verdict is due only when the waiting list asks for it (lib/attention.ts productVerdict).
  const realCard = renderToStaticMarkup(createElement(ProductLoopCard, { loop: loops[1], verdict: { state: "due", decisionKey: "product:product-results-due", itemId: "hyp_r" } }));
  const realWords = text(realCard);
  assert.ok(realWords.includes("A shorter subtitle lifts page views") && realWords.includes("Expected: Product page views rise 10%") && realWords.includes("Watched 2030-02-01 to 2030-02-28"), realWords);
  assert.ok(realCard.includes('href="/reviews?decision=product%3Aproduct-results-due#decision-item-hyp_r"'), "the verdict link opens that exact item");
  assert.ok(/<details[^>]*>.*For agents.*compound product doctor/s.test(realCard), "the command sits behind a disclosure for agents");
}

console.log("app page cases ok: results fold every spelling into one card, drafts never wait on him, a real verdict links its decision");

/* ---------- 6. Next steps said honestly: no copy-a-command, no internal words ---------- */
{
  const { NextMoves, makeLookup, nextStepsSentence } = await import("../src/components/growth/AppGrowth.tsx");
  const move = (kind, area, extra = {}) => ({
    area_id: area, area_ids: area ? [area] : [], kind, title: `${kind} ${area}`, why: "Nothing measures this today.", unblocks: 0, blocked_by: [],
    branch: "ux", skills: ["fixture-skill"], brief: `compound product growth --product product:x --area ${area}`, ...extra,
  });
  const chores = [move("connect_data", "flows"), move("record_answer", "audience"), move("set_profile", "", { brief: "compound product import <product.yaml> (add a profile block: objectives)" })];
  const apps = ["a", "b", "c"].map((id) => ({ product_id: `product:${id}`, name: id.toUpperCase(), next_moves: chores }));
  const m = mapGrowthModel({ schema_version: "compound.product_growth.v0.2", apps, areas: [{ id: "flows", title: "Main tasks", group: "g" }, { id: "audience", title: "Who it is for", group: "g" }] });
  const app = m.apps[0];
  const html = renderToStaticMarkup(createElement(NextMoves, { app, look: makeLookup(app, m.areas) }));
  const words = text(html);
  assert.ok(!/Copy (brief|import) command/.test(words), "no copy-a-command action");
  assert.ok(!words.includes("<product.yaml>") && !html.includes("&lt;product.yaml&gt;"), "a command with a placeholder is never shown");
  assert.ok(!/Pipeline:|Skills that can help/.test(words), "internal words stay out of the visible line");
  assert.ok(words.includes("See this area") && /<details[^>]*>.*For agents.*Workflow: UX/s.test(html), "agent details sit in a closed disclosure");
  const sentence = nextStepsSentence(app, m.apps);
  assert.ok(sentence.startsWith("These are mostly data chores: 3 of these 3 steps also come up for most of your 3 apps."), sentence);
  assert.ok(sentence.includes("Nothing starts them on its own"), sentence);
}

console.log("app page cases ok: next steps say they are shared chores, no copy-a-command, agent details folded");

/* ---------- 7. Overview and growth gaps: counts name what they count ---------- */
{
  const portfolio = readFileSync(new URL("../src/app/apps/page.tsx", import.meta.url), "utf8");
  const gaps = readFileSync(new URL("../src/app/growth/page.tsx", import.meta.url), "utf8");
  assert.ok(portfolio.includes('label="with a data feed"') && !portfolio.includes('label="with data"'), "the overview says what its count counts");
  assert.ok(gaps.includes('label="with any evidence on file"') && !gaps.includes('label="with data"'), "growth gaps says what its count counts");
  assert.ok(portfolio.includes('row.focus !== "hold"'), "the ranking lists only where an hour could go");
  assert.ok(portfolio.includes("proceeds90Window(") && portfolio.includes("sumBetween(app.series.proceeds_usd, window90.start, window90.end)"),
    "the proceeds panel sums the same 90 days the ranking's figure covers");
  assert.ok(portfolio.includes("#app-next-steps") && !portfolio.includes("Rule: {"), "each row leads to the app's next steps; the rule is in plain words");
  assert.ok(gaps.includes("not a to-do list"), "growth gaps says plainly it is a coverage map");
}

console.log("app page cases ok: overview and growth gaps name their counts, the ranking leads to each app's next steps");

/* ---------- 8. Marketing visuals: one quiet line where the app has none ---------- */
{
  const { ProductVisualsView } = await import("../src/components/products/VisualCampaigns.tsx");
  const { AppRouterContext } = await import("next/dist/shared/lib/app-router-context.shared-runtime.js");
  const router = { refresh() {}, push() {}, replace() {}, prefetch() {}, back() {}, forward() {} };
  const view = (report) => renderToStaticMarkup(createElement(AppRouterContext.Provider, { value: router }, createElement(ProductVisualsView, { report, resultsHref: "/apps/pocket-log#app-results" })));
  const none = text(view({ campaigns: [], pending: false, issue: "", limited: false }));
  assert.equal(none.replace(/&#x27;/g, "'"), "Marketing visuals: none in this app's open or recent work.");
  const reading = text(view({ campaigns: [], pending: true, issue: "", limited: false }));
  assert.ok(reading.includes("still reading"), "the line shows while the work read runs, never an empty heading");
}

console.log("app page cases ok: marketing visuals are one quiet line where an app has none");

/* ---------- 9. Where it lives and runs: found under every spelling of the app ---------- */
{
  const { appWhereModel } = await import("../src/components/growth/app-where.ts");
  const { AppWhere } = await import("../src/components/growth/AppWhere.tsx");
  const edge = (from, rel, to, to_label, confidence = "observed", extra = {}) => ({ from, from_kind: "product", rel, rel_title: rel.replace("_", " "), to, to_label, confidence, evidence: "apps/pocket-log/deploy.sh:12", carried: false, ...extra });
  const graph = {
    snapshot: { state: "fresh", observed_at: new Date(NOW - 2 * DAY).toISOString() },
    edge_list: [
      edge("product:pocket_log", "code_in", "repo:example.invalid/pocket-log", "example.invalid/pocket-log"),
      edge("product:pocket_log", "deployed_on", "host:box-1", "Box one (example host)", "declared"),
      edge("product:Pocket-Log", "calls", "host:api", "API host"),
      edge("product:pocket_log", "uses", "service:mail", "Mail"),
      edge("product:tide_chart", "code_in", "repo:example.invalid/tide", "example.invalid/tide"),
    ],
  };
  const model = appWhereModel(graph, ["product:pocket-log"], NOW);
  assert.deepEqual(model.links.map((l) => [l.label, l.target, l.confidence]), [
    ["Code in", "example.invalid/pocket-log", "observed"],
    ["Deployed on", "Box one (example host)", "declared"],
    ["Calls", "API host", "observed"],
  ], "the underscore and upper-case spellings in the graph are this app's links");
  assert.deepEqual(model.other, [{ relation: "uses", count: 1 }]);
  assert.equal(model.observedDaysAgo, 2);
  const words = text(renderToStaticMarkup(createElement(AppWhere, { name: "Pocket Log", model, read: { status: "live", source: "x" } })));
  assert.ok(words.includes("Inventory observed 2 days ago") && words.includes("Written down, not seen"), words);
  const unread = appWhereModel(null, ["product:pocket-log"], NOW);
  assert.equal(unread.links, null, "an unread graph is unknown, never 'no links'");
  const empty = text(renderToStaticMarkup(createElement(AppWhere, { name: "Quiet App", model: appWhereModel({ snapshot: {}, edge_list: [] }, ["product:quiet-app"], NOW), read: { status: "live", source: "x" } })));
  assert.ok(empty.includes("That does not prove it has none"), empty);
}

console.log("app page cases ok: where an app lives and runs is found under every spelling, observed or declared, with its age");
