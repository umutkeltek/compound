// Shell cases: the frame every page sits in (sidebar, section tabs,
// breadcrumb, page header). Each case fails on the earlier shell.
//
//  1. The menu is six sections named after what the operator asks, in order,
//     and every page address on disk belongs to one section and one tab.
//  2. The sidebar shows only the sections and one count (what waits for him,
//     on Today): no Work count, no page counts, no "+" that adds nothing, and
//     the app he is on stays visible.
//  3. Pages that pass an earlier name to PageHeader still get their page, and
//     the page header ends with its section's pages as tabs.
//  4. The breadcrumb says section, then page, then item, and an unknown
//     address says it is not found.
//  5. Search finds pages by name, earlier name and purpose; apps by name and
//     alias; waiting decisions by the words Today shows; open work by title.
//     Each result says its kind and links through lib/links.ts; a read still
//     loading says so; Enter opens, arrows move, Escape closes (Radix dialog).
//  6. A wrong address gets a console page with a way back to the right list
//     (root, an unknown app, an unknown work item); a failed page offers
//     "Try again"; the shared loading frame mirrors no single page.
//  7. Nothing floats over the content: the Ask button and its free-text box
//     are gone, and their canned views are named quick views in search.
//  8. The browser connection left the top bar for Settings and says it is
//     not used for anything yet, in the one shared sentence.
//
// Run: bun scripts/shell-cases.mjs
import assert from "node:assert/strict";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { AppRouterContext } from "next/dist/shared/lib/app-router-context.shared-runtime.js";
import { PathnameContext, SearchParamsContext } from "next/dist/shared/lib/hooks-client-context.shared-runtime.js";

const experience = await import("../src/lib/experience.ts");
const { NAV_SECTIONS, placeForPath, placeForName, sectionTabs } = experience;
const { Nav } = await import("../src/components/Nav.tsx");
const { NavAppLinks, visibleApps } = await import("../src/components/NavAppLinks.tsx");
const { PageHeader } = await import("../src/components/kit/PageHeader.tsx");
const { breadcrumbFor } = await import("../src/components/kit/Breadcrumb.tsx");
const { searchResults } = await import("../src/components/CommandK.tsx");
const { buildSearchIndex } = await import("../src/app/api/search/build.ts");

const router = Object.fromEntries(["push", "replace", "refresh", "back", "forward", "prefetch"].map((k) => [k, () => {}]));
const render = (path, element) =>
  renderToStaticMarkup(
    createElement(AppRouterContext.Provider, { value: router },
      createElement(PathnameContext.Provider, { value: path },
        createElement(SearchParamsContext.Provider, { value: new URLSearchParams() }, element))));
const count = (html, needle) => html.split(needle).length - 1;
// Short failure messages: never dump a whole page into the log.
const has = (html, re, msg) => assert.ok(re.test(html), msg ?? `expected ${re}`);
const hasNot = (html, re, msg) => assert.ok(!re.test(html), msg ?? `unexpected ${re}`);

// ---------- 1. six sections, in his words, and every address placed ----------
assert.deepEqual(
  NAV_SECTIONS.map((s) => s.label),
  ["Today", "Apps", "Work", "Agents", "Knowledge", "Settings"],
  "the menu is six sections in this order",
);
for (const section of NAV_SECTIONS) {
  assert.match(section.question, /\?$/, `${section.label} names the question it answers`);
  for (const page of section.pages) {
    assert.ok(page.description.trim().length > 10, `${page.label} has a plain purpose line`);
  }
}

const expected = {
  "/": ["today", "Today"],
  "/reviews": ["today", "All decisions"],
  "/apps": ["apps", "All apps"],
  "/apps/sample-app": ["apps", "All apps"],
  "/growth": ["apps", "Growth gaps"],
  "/loops": ["apps", "Results"],
  "/ads": ["apps", "Ads"],
  "/work": ["work", "Work"],
  "/work/work_sample": ["work", "Work"],
  "/flow": ["work", "Work"],
  "/projects": ["work", "Projects"],
  "/live": ["agents", "Working now"],
  "/executors": ["agents", "All agents"],
  "/traffic": ["agents", "Waiting on each other"],
  "/memory": ["knowledge", "Memory"],
  "/map/memory": ["knowledge", "Memory"],
  "/standing-law": ["knowledge", "Rules"],
  "/sources": ["knowledge", "Sources"],
  "/settings": ["settings", "Settings"],
  "/routing": ["settings", "Models and cost"],
  "/map": ["settings", "Skills and tools"],
  "/map/lineage": ["settings", "Evidence history"],
  "/map/lineage/rec_sample": ["settings", "Evidence history"],
  "/representation": ["settings", "View library"],
};
for (const [path, [section, label]] of Object.entries(expected)) {
  const place = placeForPath(path);
  assert.ok(place, `${path} belongs to a section`);
  assert.equal(place.section.id, section, `${path} section`);
  assert.equal(place.page.label, label, `${path} page`);
}
assert.equal(placeForPath("/nonsense"), null, "an address the console does not have is not given a page name");

// Every page.tsx on disk is placed: a new page cannot ship outside the menu.
const appDir = fileURLToPath(new URL("../src/app/", import.meta.url));
function pageRoutes(dir) {
  const out = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      if (entry === "api") continue;
      out.push(...pageRoutes(full));
    } else if (entry === "page.tsx") {
      const rel = relative(appDir, dir).split(sep).filter(Boolean).filter((seg) => !/^\(.*\)$/.test(seg));
      out.push("/" + rel.map((seg) => (seg.startsWith("[") ? "sample_segment" : seg)).join("/"));
    }
  }
  return out;
}
const routes = pageRoutes(appDir);
assert.ok(routes.length >= 20, "found the console's pages");
for (const route of routes) {
  assert.ok(placeForPath(route), `${route} has a section and a tab in lib/experience.ts`);
}

// The route table, as the report prints it.
const table = Object.keys(expected).map((path) => {
  const place = placeForPath(path);
  return `${path} -> ${place.section.label} / ${place.page.label}`;
});
assert.equal(table.length, Object.keys(expected).length);

// Tabs: one row per multi-page section; Work has one page and no row; the
// Settings back room holds the pages agents use; Projects left the menu.
assert.deepEqual(sectionTabs(NAV_SECTIONS[0]).main.map((p) => p.label), ["Today", "All decisions"]);
assert.deepEqual(sectionTabs(NAV_SECTIONS[1]).main.map((p) => p.label), ["All apps", "Growth gaps", "Results", "Ads"]);
assert.deepEqual(sectionTabs(NAV_SECTIONS[2]).main, [], "a one-page section shows no tab row");
assert.deepEqual(sectionTabs(NAV_SECTIONS[3]).main.map((p) => p.label), ["Working now", "All agents", "Waiting on each other"]);
assert.deepEqual(sectionTabs(NAV_SECTIONS[4]).main.map((p) => p.label), ["Memory", "Rules", "Sources"]);
assert.deepEqual(sectionTabs(NAV_SECTIONS[5]).main.map((p) => p.label), ["Settings", "Models and cost"]);
assert.deepEqual(sectionTabs(NAV_SECTIONS[5]).backRoom.map((p) => p.label), ["Skills and tools", "Evidence history", "View library"]);

// ---------- 2. the sidebar ----------
const apps = Array.from({ length: 9 }, (_, i) => ({
  slug: `sample-app-${i}`, name: `Sample App ${i}`, hue: "#777777", stale: false, noData: false, kind: "app",
}));
const sidebar = render(
  "/apps/sample-app-8",
  createElement(Nav, {
    decideCount: 7,
    workCount: 3,
    apps: createElement(NavAppLinks, { apps, total: apps.length }),
  }),
);
assert.equal(count(sidebar, "data-nav-section="), 6, "the sidebar lists the six sections");
assert.deepEqual(
  [...sidebar.matchAll(/data-nav-section="([a-z]+)"/g)].map((m) => m[1]),
  ["today", "apps", "work", "agents", "knowledge", "settings"],
);
assert.equal(count(sidebar, "data-nav-count="), 1, "one count in the sidebar");
has(sidebar, /data-nav-count="waiting"[^>]*>7</, "the count is what waits for him");
has(sidebar, /7 decisions wait for you/, "the count says what it counts, in the pages' words");
hasNot(sidebar, /decision group/, "the tooltip uses the pages' word");
hasNot(sidebar, />3</, "no Work count");
hasNot(sidebar, /Essentials|>System<|>Growth</, "no machine-part group names");
hasNot(sidebar, /href="\/growth"/, "no '+' that opens Growth; Growth is a tab under Apps");
has(sidebar, /data-active="true"[^>]*data-nav-section="apps"/, "the Apps section is active on an app page");
has(sidebar, /<a [^>]*aria-current="page"[^>]*href="\/apps\/sample-app-8"/, "the app he is on is shown, though it ranks ninth");
assert.equal(visibleApps(apps, "sample-app-8", false).length, 7, "six plus the current app");
assert.equal(visibleApps(apps, "sample-app-2", false).length, 6);
has(sidebar, /Show 2 more/, "the hidden count leaves out the app already shown");

// ---------- 3. PageHeader: earlier names keep working, tabs under the header ----------
for (const [old, label] of [
  ["Decide", "All decisions"], ["Portfolio", "All apps"], ["Growth", "Growth gaps"], ["Live", "Working now"],
  ["Agents", "All agents"], ["Coordination", "Waiting on each other"], ["Routing", "Models and cost"],
  ["Map", "Skills and tools"], ["Views", "View library"], ["Today", "Today"], ["Work", "Work"],
  ["Projects", "Projects"], ["Memory", "Memory"], ["Rules", "Rules"], ["Sources", "Sources"],
  ["Settings", "Settings"], ["Results", "Results"], ["Ads", "Ads"],
]) {
  assert.equal(placeForName(old)?.page.label, label, `PageHeader name="${old}" still finds its page`);
}
const decide = render("/reviews", createElement(PageHeader, { name: "Decide" }));
has(decide, /<h1[^>]*>All decisions<\/h1>/, "the H1 is the registry name");
has(decide, /data-section-tabs="today"/, "the header ends with the section's tabs");
has(decide, /href="\/"[^>]*>Today</);
has(decide, /href="\/reviews"[^>]*aria-current="page"|aria-current="page"[^>]*href="\/reviews"/, "the open tab is marked");
assert.ok(decide.indexOf("<h1") < decide.indexOf("data-section-tabs"), "tabs sit under the title");
const work = render("/work", createElement(PageHeader, { name: "Work" }));
hasNot(work, /data-section-tabs/, "Work has one page and no tab row");
const lineage = render("/map/lineage/rec_sample", createElement(PageHeader, { name: "Map", title: "Evidence history" }));
has(lineage, /data-back-room/, "the back room cluster shows on its pages");
has(lineage, /aria-current="page"[^>]*>Evidence history<|Evidence history<\/a>/);
const appPage = render("/apps/sample-app-1", createElement(PageHeader, { name: "Portfolio", title: "Sample App 1" }));
has(appPage, />Apps<\/div>/, "an item page names its section above the title");
has(appPage, /data-section-tabs="apps"/);

// ---------- 4. breadcrumb ----------
const crumbs = (path) => breadcrumbFor(path).map((c) => c.label);
assert.deepEqual(crumbs("/"), ["Today"]);
assert.deepEqual(crumbs("/reviews"), ["Today", "All decisions"]);
assert.deepEqual(crumbs("/apps"), ["Apps", "All apps"]);
assert.deepEqual(crumbs("/apps/sample-app"), ["Apps", "All apps", "Sample App"]);
assert.deepEqual(crumbs("/work"), ["Work"]);
assert.deepEqual(crumbs("/work/work_sample"), ["Work", "work_sample"]);
assert.deepEqual(crumbs("/map/lineage/rec_sample"), ["Settings", "Evidence history", "rec_sample"]);
assert.deepEqual(crumbs("/nonsense"), ["Page not found"]);
assert.equal(breadcrumbFor("/reviews")[0].href, "/", "the section crumb leads to the section");
assert.equal(breadcrumbFor("/reviews").at(-1).href, undefined, "the last crumb is where he is, not a link");

// ---------- 5. search ----------
const labelsOf = (groups, id) => (groups.find((g) => g.id === id)?.rows ?? []).map((r) => r.label);
const hrefsOf = (groups, id) => (groups.find((g) => g.id === id)?.rows ?? []).map((r) => r.href);
// Pages by name, earlier name and purpose; the old palette knew only labels.
for (const [query, label] of [
  ["decisions", "All decisions"], ["decide", "All decisions"], ["permissions", "Settings"],
  ["blocked", "Work"], ["portfolio", "All apps"], ["coordination", "Waiting on each other"],
  ["cost per install", "Ads"], ["where knowledge", "Sources"],
]) {
  assert.ok(labelsOf(searchResults(query, null), "pages").includes(label), `"${query}" finds the ${label} page`);
}
const index = buildSearchIndex({
  growth: {
    status: "live",
    value: {
      schema_version: "compound.product_growth.v1",
      apps: [
        { product_id: "product:sample-weather", name: "Sample Weather", aliases: ["skycast"] },
        { product_id: "product:fixture-notes", name: "Fixture Notes", aliases: [] },
      ],
    },
  },
  attention: {
    status: "stale",
    report: {
      groups: [
        { key: "fixture:decision-one", area: "product-results/fixture", band: "needs_you", count: 3, operator_summary: "Approve the new onboarding text for the weather app." },
        { key: "fixture:not-mine", area: "upkeep", band: "agent_work", count: 1, operator_summary: "Rotate the fixture cache." },
      ],
    },
  },
  work: {
    status: "live",
    value: { open: [
      { work_id: "work_fx_one", state: "held", title: "Fix the sync gap in the notes widget" },
      { work_id: "work_fx_two", state: "running", success_predicate: "Weather alerts arrive on time" },
    ] },
  },
  projects: {
    byWork: { work_fx_one: "product:fixture-notes", work_fx_two: "none" },
    projects: [
      { id: "product:fixture-notes", name: "Fixture Notes", kind: "app", open: 1, needYou: 1, inProgress: 0, waitingForCheck: 0, stale: 0 },
      { id: "none", name: "Not linked to a project", kind: "none", open: 1, needYou: 0, inProgress: 1, waitingForCheck: 0, stale: 0 },
    ],
    total: { open: 2, needYou: 1, inProgress: 1, waitingForCheck: 0, stale: 0 },
  },
});
let hits = searchResults("skycast", index);
assert.deepEqual(labelsOf(hits, "apps"), ["Sample Weather"], "an app is found by its alias");
assert.deepEqual(hrefsOf(hits, "apps"), ["/apps/sample-weather"], "the app links through appHref");
hits = searchResults("onboarding text", index);
assert.equal(labelsOf(hits, "decisions").length, 1, "a waiting decision is found by its displayed words");
assert.equal(hrefsOf(hits, "decisions")[0], "/reviews?decision=fixture%3Adecision-one", "the decision links through decideHref");
assert.equal(labelsOf(searchResults("rotate", index), "decisions").length, 0, "only decisions that wait for him");
hits = searchResults("sync gap", index);
assert.deepEqual(labelsOf(hits, "work"), ["Fix the sync gap in the notes widget"], "open work is found by its title");
assert.deepEqual(hrefsOf(hits, "work"), ["/work/work_fx_one"], "work links through workItemHref");
assert.match(hits.find((g) => g.id === "work").rows[0].sub, /^Fixture Notes · /, "a work result names its app");
assert.equal(labelsOf(searchResults("fixture notes", index), "work").length, 1, "work is found by its app's name");
assert.equal(labelsOf(searchResults("weather", index), "work")[0], "Weather alerts arrive on time");
const kinds = searchResults("weather", index).flatMap((g) => g.rows.map((r) => r.kind));
assert.ok(kinds.includes("app") && kinds.includes("decision") && kinds.includes("work"), "one query reaches every kind");
// Before the index lands, every group says it is still reading; never "nothing".
const cold = searchResults("weather", null);
for (const id of ["apps", "decisions", "work"]) {
  assert.match(cold.find((g) => g.id === id)?.note ?? "", /Still reading/, `${id} says it is still reading`);
}
assert.equal(searchResults("weather", index).find((g) => g.id === "apps").note, "", "a landed read has no note");
// No terminal commands among the results; Enter and arrows are handled; Radix dialog keeps focus inside.
const paletteSource = readFileSync(fileURLToPath(new URL("../src/components/CommandK.tsx", import.meta.url)), "utf8");
assert.doesNotMatch(paletteSource, /kind: "cmd"|clipboard/, "search sends nobody to a terminal");
assert.match(paletteSource, /e\.key === "Enter"/);
assert.match(paletteSource, /e\.key === "ArrowDown"/);
assert.match(paletteSource, /<Dialog\.Root/);

// ---------- 6. wrong addresses, failures, loading ----------
const pageModule = async (rel) => (await import(`../src/app/${rel}`)).default;
const notFoundRoot = render("/nonsense", createElement(await pageModule("not-found.tsx")));
has(notFoundRoot, /There is no page at this address/, "the root not-found says what happened");
has(notFoundRoot, /href="\/"[^>]*>Go to Today/, "and leads back to Today");
const notFoundApp = render("/apps/no-such-app", createElement(await pageModule("apps/[slug]/not-found.tsx")));
has(notFoundApp, /No app at this address/);
has(notFoundApp, /href="\/apps"[^>]*>See all apps/, "an unknown app leads to the app list");
const notFoundWork = render("/work/work_missing", createElement(await pageModule("work/[id]/not-found.tsx")));
has(notFoundWork, /No work item opened at this address/);
has(notFoundWork, /could not be read just now/, "it does not claim the item is gone when the read may have failed");
has(notFoundWork, /href="\/work\?lens=table&amp;filter=all"[^>]*>See all work/, "an unknown work item leads to the work list");
const failed = render("/apps", createElement(await pageModule("error.tsx"), { error: Object.assign(new Error("fixture"), { digest: "fixture-digest" }), reset: () => {} }));
has(failed, />Try again</, "a failed page offers Try again");
has(failed, /href="\/"[^>]*>Go to Today/);
hasNot(failed, /fixture<|>fixture /, "the raw error message is not shown");
const loadingFrame = render("/apps", createElement(await pageModule("loading.tsx")));
has(loadingFrame, /data-skeleton="page"/, "the shared loading frame is the neutral page shape");
hasNot(loadingFrame, /minmax\(300px,2fr\)/, "not an earlier Today layout");

// ---------- 7. no floating Ask; quick views in search ----------
const readSrc = (rel) => readFileSync(fileURLToPath(new URL(`../src/${rel}`, import.meta.url)), "utf8");
const exists = (rel) => { try { statSync(fileURLToPath(new URL(`../src/${rel}`, import.meta.url))); return true; } catch { return false; } };
const layoutSource = readSrc("app/layout.tsx");
assert.ok(!exists("components/AskCompound.tsx"), "the Ask Compound button and its text box are gone");
assert.doesNotMatch(layoutSource, /AskCompound/, "the layout mounts no Ask button");
for (const rel of ["app/layout.tsx", "components/Nav.tsx", "components/NavContainer.tsx", "components/SystemBanner.tsx", "components/OperatorConnection.tsx", "components/RefreshControl.tsx"]) {
  assert.doesNotMatch(readSrc(rel), /fixed (?:bottom|right|left)-\d/, `${rel} floats nothing over page content`);
}
const quick = searchResults("", index).find((g) => g.id === "views");
assert.deepEqual(quick?.rows.map((r) => r.label), ["Most important decision", "Work on hold", "Work running now"], "search opens on the quick views");
assert.equal(quick.rows[0].href, "/reviews?decision=fixture%3Adecision-one", "the most important decision opens that decision");
assert.equal(quick.rows[0].sub, "Approve the new onboarding text for the weather app.", "and says which one");
assert.ok(labelsOf(searchResults("why is work blocked", index), "views").includes("Work on hold"), "an old Ask question finds its quick view");
assert.equal(searchResults("", null).find((g) => g.id === "views").rows.length, 2, "before the index lands, the fixed quick views still show");

// ---------- 8. the browser connection lives on Settings ----------
const header = layoutSource.slice(layoutSource.indexOf("<header"), layoutSource.indexOf("</header>"));
assert.doesNotMatch(header, /OperatorConnection/, "the connect control left the top bar");
const connection = readSrc("components/OperatorConnection.tsx");
assert.match(connection, /pathname === "\/settings"/, "it shows on Settings");
assert.match(connection, /not used for anything yet/, "it says honestly that the connection unlocks nothing");
assert.doesNotMatch(connection, /Connect operator/, "no unexplained 'Connect operator'");
const { CONSOLE_ACTS_SENTENCE } = await import("../src/components/OperatorConnection.tsx");
assert.match(CONSOLE_ACTS_SENTENCE, /shows and explains/);
assert.match(CONSOLE_ACTS_SENTENCE, /Acting from it is being built/);

console.log("shell-cases ok: six sections in his words, every page placed with a tab, one sidebar count, current app shown, earlier header names kept, section › page › item breadcrumb; search finds pages, apps, decisions and work; wrong addresses and failures lead back; nothing floats, quick views in search; browser connection on Settings");
console.log(table.join("\n"));
