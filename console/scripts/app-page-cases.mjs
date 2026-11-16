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
