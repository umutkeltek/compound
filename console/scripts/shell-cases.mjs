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
//
// Run: bun scripts/shell-cases.mjs
import assert from "node:assert/strict";
import { readdirSync, statSync } from "node:fs";
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

console.log("shell-cases ok: six sections in his words, every page placed with a tab, one sidebar count, current app shown, earlier header names kept, section › page › item breadcrumb");
console.log(table.join("\n"));
