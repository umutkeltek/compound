// Falsifiers for the link builders (lib/links.ts): one canonical URL per
// destination, and one parser for the Work list parameters, so the page that
// reads a parameter and the pages that link to it cannot drift apart.
// Run: bun scripts/links-cases.mjs. The wired gate runs it:
// work-legibility-smoke.mjs imports it, and a failing case fails that smoke.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const links = await import("../src/lib/links.ts");
const {
  workListHref, parseWorkListParams, workItemHref, decideHref, appHref, adsHref, settingsHref,
  memoryReviewHref, memoryCandidateHref, evidenceHistoryHref, WORK_LENSES, WORK_FILTERS,
} = links;

// Work list: defaults are left out, a filter opens the List, values are canonical.
assert.equal(workListHref(), "/work");
assert.equal(workListHref({ lens: "board" }), "/work");
assert.equal(workListHref({ lens: "list" }), "/work?lens=table", "list is another name for table");
assert.equal(workListHref({ lens: "table", filter: "focus" }), "/work?lens=table", "the default filter is left out");
assert.equal(workListHref({ lens: "table", filter: "stale" }), "/work?lens=table&filter=stale");
assert.equal(workListHref({ filter: "stale" }), "/work?lens=table&filter=stale", "a filter without a lens opens the List");
assert.equal(workListHref({ filter: "blocked" }), "/work?lens=table&filter=blocked");
assert.equal(workListHref({ filter: "running" }), "/work?lens=table&filter=running");
for (const owner of ["Fixture Agent", "fixture-agent", "harness:fixture-agent", "fixture_agent:lane-3"]) {
  assert.equal(workListHref({ lens: "table", owner }), "/work?lens=table&owner=fixture_agent", `owner ${owner} is written as its agent key`);
}
assert.equal(workListHref({ app: "product:fixture-app" }), "/work?app=fixture-app");
assert.equal(workListHref({ app: "fixture-app", q: "  two words " }), "/work?app=fixture-app&q=two%20words");
assert.equal(workListHref({ trace: "work_fixture:one" }), "/work?trace=work_fixture%3Aone");
assert.equal(workListHref({ trace: "<id>" }), "/work", "a value that is not a work id is never written");
assert.equal(
  workListHref({ q: "x", app: "fixture-app", owner: "fixture_agent", filter: "stale", lens: "table" }),
  "/work?lens=table&filter=stale&owner=fixture_agent&app=fixture-app&q=x",
  "parameters come in one fixed order",
);

// The parser: defaults, aliases, case, and both input shapes a page receives.
assert.deepEqual(parseWorkListParams({}), { lens: "board", filter: "focus", owner: "", app: "", project: "", q: "", trace: "" });
assert.deepEqual(parseWorkListParams(null), { lens: "board", filter: "focus", owner: "", app: "", project: "", q: "", trace: "" });
assert.deepEqual(
  parseWorkListParams({ lens: ["list", "board"], filter: "Stale", owner: "Fixture-Agent:lane-1", app: "product:fixture-app", q: " find me ", trace: "not a work id" }),
  { lens: "table", filter: "stale", owner: "fixture_agent", app: "fixture-app", project: "", q: "find me", trace: "" },
);
assert.deepEqual(parseWorkListParams({ lens: "nonsense", filter: "nonsense" }), { lens: "board", filter: "focus", owner: "", app: "", project: "", q: "", trace: "" });
assert.equal(parseWorkListParams(new URLSearchParams("owner=unassigned")).owner, "unassigned", "the no-agent filter has its own key");

// Round trip: what a builder writes, the parser reads back.
const roundTrips = [
  {},
  { lens: "table", filter: "stale" },
  { filter: "blocked", owner: "relay_bot" },
  { lens: "space", app: "fixture-app", q: "a & b = c?" },
  { lens: "table", filter: "all", trace: "work_fixture_0a1b" },
];
for (const params of roundTrips) {
  const href = workListHref(params);
  const parsed = parseWorkListParams(new URL(href, "https://console.example").searchParams);
  assert.equal(workListHref(parsed), href, `round trip of ${href}`);
}

// The parser accepts every lens and filter the Work page reads today
// (WorkLenses / work/page.tsx and WorkTableLens), so links built here open
// what the page shows. A new view or filter on the page must be added here.
const workPage = readFileSync(new URL("../src/app/work/page.tsx", import.meta.url), "utf8");
assert.match(workPage, /parseWorkListParams\(await searchParams\)/, "the Work page reads its parameters through the one parser");
const workShell = readFileSync(new URL("../src/components/work/WorkLenses.tsx", import.meta.url), "utf8");
const pageLenses = [...(/SHOWN_LENSES[^=]*= new Set<WorkLens>\(\[([^\]]+)\]/.exec(workShell)?.[1] ?? "").matchAll(/"([a-z]+)"/g)].map((m) => m[1]);
assert.ok(pageLenses.length >= 3, `found the Work page's views: ${pageLenses}`);
for (const lens of pageLenses) assert.ok(WORK_LENSES.includes(lens), `lens ${lens} read by the Work page is known to the parser`);
const tableLens = readFileSync(new URL("../src/components/work/WorkTableLens.tsx", import.meta.url), "utf8");
const tableFilters = [...(/const FILTERS\b[^=]*= \[([\s\S]*?)\];/.exec(tableLens)?.[1] ?? "").matchAll(/id: "([a-z]+)"/g)].map((m) => m[1]);
assert.ok(tableFilters.length >= 5, `found the List's filters: ${tableFilters}`);
for (const filter of tableFilters) assert.ok(WORK_FILTERS.includes(filter), `filter ${filter} read by the List is known to the parser`);

// One work item, Decide, apps, Ads.
assert.equal(workItemHref("work_fixture_0a1b"), "/work/work_fixture_0a1b");
assert.equal(workItemHref("work_fixture:one"), "/work/work_fixture%3Aone");
assert.equal(workItemHref(""), "/work", "an empty id never makes a dead link");
assert.equal(decideHref(), "/reviews");
const key = "fixture group:key one";
assert.equal(decideHref({ decision: key }), `/reviews?decision=${encodeURIComponent(key)}`, "same URL the Decide links write by hand today");
assert.equal(decideHref({ area: "fixture-area", q: "copy" }), "/reviews?area=fixture-area&q=copy");
assert.equal(appHref("product:fixture-app"), "/apps/fixture-app");
assert.equal(appHref("fixture-app"), "/apps/fixture-app");
assert.equal(appHref(""), "/apps");
assert.equal(adsHref(), "/ads");
assert.equal(adsHref({ product: "fixture-app", window: 30 }), "/ads?product=product%3Afixture-app&window=30");
assert.equal(adsHref({ product: "product:fixture-app", window: "90d" }), "/ads?product=product%3Afixture-app&window=90d");
assert.equal(adsHref({ window: "soon" }), "/ads", "a window that is not a number of days is never written");

// Settings, Memory, evidence history.
assert.equal(settingsHref("safety-permissions"), "/settings?section=safety-permissions#settings-safety-permissions");
assert.equal(settingsHref(""), "/settings");
assert.equal(memoryReviewHref(), "/memory#memory-review");
assert.equal(memoryCandidateHref("mc_fixture_1"), "/memory#mc_fixture_1");
assert.equal(memoryCandidateHref(""), "/memory#memory-review");
assert.equal(evidenceHistoryHref("ev_0123456789ab"), "/map/lineage/ev_0123456789ab");
assert.equal(evidenceHistoryHref(""), "/map");

// A record reference links only where a page opens it; the rest are words, never a link to "not found".
{
  const { recordHref } = await import("../src/lib/links.ts");
  const { recordRefWords } = await import("../src/lib/plain-words.ts");
  assert.equal(recordHref("work_fixture_item"), "/work/work_fixture_item");
  assert.equal(recordHref("work:work_fixture_item"), "/work/work_fixture_item");
  assert.equal(recordHref("ev_0123456789ab"), "/map/lineage/ev_0123456789ab");
  assert.equal(recordHref("evidence:ev_0123456789ab"), "/map/lineage/ev_0123456789ab");
  assert.equal(recordHref("cand_0123456789ab"), "/map/lineage/cand_0123456789ab");
  assert.equal(recordHref("https://docs.example/page"), "https://docs.example/page");
  for (const ref of ["ratt_20300101T000000Z_fixture1", "land-check:landcheck_fixture01", "commit:0123456789abcdef0123", "rec_fixture", "tr_fixture",
    "proof-check:pfrs_fixture", "presubmit-execution:artifact:sha256:00", "/srv/fixture/notes.md", "finding:find_fixture", "ctx_fixture"]) {
    assert.equal(recordHref(ref), null, `${ref} has no page that reads it`);
  }
  assert.deepEqual(recordRefWords("ratt_20300101T000000Z_fixture1"), { kind: "Agent run", id: "ratt_20300101T000000Z_fixture1" });
  assert.deepEqual(recordRefWords("land-check:landcheck_fixture01"), { kind: "Landing check", id: "landcheck_fixture01" });
  assert.deepEqual(recordRefWords("commit:0123456789abcdef0123"), { kind: "Commit", id: "0123456789ab" });
  assert.deepEqual(recordRefWords("presubmit-execution:artifact:sha256:00"), { kind: "Test run before landing", id: "artifact:sha256:00" });
  assert.deepEqual(recordRefWords("/srv/fixture/notes.md"), { kind: "File", id: "notes.md" });
  assert.deepEqual(recordRefWords("https://docs.example/page"), { kind: "Web page", id: "docs.example/page" });
  assert.deepEqual(recordRefWords("something-else"), { kind: "Record", id: "something-else" });
}

// No builder ever writes a placeholder such as <id>.
for (const href of [workListHref({ trace: "<id>" }), workItemHref(" "), appHref(" "), memoryCandidateHref(" "), evidenceHistoryHref(" ")]) {
  assert.doesNotMatch(href, /[<>]|\/$/, `no placeholder or dangling slash in ${href}`);
}

console.log("links cases ok: Work list builder and parser agree (round trip, aliases, defaults, every lens and filter the page reads); item, Decide, app, Ads, Settings, Memory and evidence links are canonical");
