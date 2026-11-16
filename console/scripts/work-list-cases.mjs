// Work List cases (lib/work-list.ts): which rows each filter, agent, app or
// group and search keeps, the order, the "by app" links, and where an old
// Projects address goes. Pure functions; run inside the wired
// work-legibility smoke, or directly: bun scripts/work-list-cases.mjs

import assert from "node:assert/strict";

const { filterWorkList, matchesWorkFilter, projectsRedirectHref } = await import("../src/lib/work-list.ts");
const { parseWorkListParams, workGroupHref, workListHref } = await import("../src/lib/links.ts");

const DAY = 86_400_000;
const NOW = Date.parse("2030-03-01T00:00:00Z");
const row = (workId, state, extra = {}) => ({
  workId, title: `Fixture ${workId}`, state, owner: "", ownerName: "No agent",
  projectId: "none", projectKind: "none", projectName: "Not linked to a project",
  stale: false, pickedUpMs: null, ...extra,
});
const rows = [
  row("work_a_held", "held", { projectId: "product:fixture-app", projectKind: "app", projectName: "Fixture App" }),
  row("work_b_run_old", "running", { stale: true, pickedUpMs: NOW - 40 * DAY, owner: "fixture_agent:lane-1", projectId: "product:fixture-app", projectKind: "app", projectName: "Fixture App" }),
  row("work_c_check_older", "verifying", { stale: true, pickedUpMs: NOW - 60 * DAY, projectId: "codebase:fixture-tools", projectKind: "codebase", projectName: "fixture-tools" }),
  row("work_d_claimed", "claimed", { pickedUpMs: NOW - 2 * DAY, owner: "Fixture-Agent" }),
  row("work_e_ready", "ready"),
  row("work_f_blocked", "blocked", { owner: "operator:someone" }),
];
const ids = (result) => result.rows.map((r) => r.workId);
const list = (query, known = true) => filterWorkList(rows, parseWorkListParams(query), { projectsKnown: known });

// Each filter keeps exactly its rows.
assert.deepEqual(ids(list({})), ["work_a_held", "work_f_blocked", "work_c_check_older", "work_b_run_old"], "default: on hold first, then not moved, oldest first");
assert.deepEqual(ids(list({ filter: "blocked" })), ["work_a_held", "work_f_blocked"]);
assert.deepEqual(ids(list({ filter: "stale" })), ["work_c_check_older", "work_b_run_old"], "the stale list is oldest first");
assert.deepEqual(ids(list({ filter: "running" })), ["work_b_run_old"]);
assert.deepEqual(ids(list({ filter: "claimed" })), ["work_d_claimed"]);
assert.deepEqual(ids(list({ filter: "verifying" })), ["work_c_check_older"]);
assert.deepEqual(ids(list({ filter: "progress" })).sort(), ["work_b_run_old", "work_c_check_older", "work_d_claimed"]);
assert.equal(list({ filter: "all" }).rows.length, rows.length);
assert.equal(matchesWorkFilter({ state: "running", stale: false }, "focus"), false, "fresh running work is not in focus");

// Agent, app, group and search narrow the list; an agent in any spelling.
assert.deepEqual(ids(list({ filter: "all", owner: "Fixture Agent" })), ["work_b_run_old", "work_d_claimed"]);
assert.deepEqual(ids(list({ filter: "all", owner: "operator" })), ["work_f_blocked"]);
assert.deepEqual(ids(list({ filter: "all", app: "product:fixture-app" })), ["work_a_held", "work_b_run_old"]);
assert.deepEqual(ids(list({ filter: "stale", app: "fixture-app" })), ["work_b_run_old"]);
assert.deepEqual(ids(list({ filter: "all", project: "codebase:fixture-tools" })), ["work_c_check_older"]);
assert.deepEqual(ids(list({ filter: "all", project: "none" })), ["work_f_blocked", "work_d_claimed", "work_e_ready"], "on hold first, then known pick-up times");
assert.deepEqual(ids(list({ filter: "all", q: "fixture app" })), ["work_a_held", "work_b_run_old"], "search finds the app's name");

// An app filter before the groups are read keeps nothing and says so; it never shows every row.
assert.deepEqual(list({ filter: "all", app: "fixture-app" }, false), { rows: [], waitingForGroups: true });
assert.equal(list({ filter: "all" }, false).rows.length, rows.length, "no app filter: the groups are not needed");

// Links from the "by app" view: an app by slug, any other group by id; each count by its filter.
assert.equal(workGroupHref({ id: "product:fixture-app", kind: "app" }), "/work?lens=table&filter=all&app=fixture-app");
assert.equal(workGroupHref({ id: "product:fixture-app", kind: "app" }, "stale"), "/work?lens=table&filter=stale&app=fixture-app");
assert.equal(workGroupHref({ id: "codebase:fixture-tools", kind: "codebase" }, "blocked"), "/work?lens=table&filter=blocked&project=codebase%3Afixture-tools");
assert.equal(workGroupHref({ id: "none", kind: "none" }), "/work?lens=table&filter=all&project=none");
assert.equal(workListHref({ lens: "project" }), "/work?lens=project");
for (const href of [workGroupHref({ id: "codebase:fixture-tools", kind: "codebase" }, "progress"), workGroupHref({ id: "product:fixture-app", kind: "app" }, "claimed")]) {
  const parsed = parseWorkListParams(new URL(href, "https://console.invalid").searchParams);
  assert.equal(workListHref(parsed), href, `round trip ${href}`);
}

// An old Projects address keeps working.
assert.equal(projectsRedirectHref({ scope: "product:fixture-app" }), "/apps/fixture-app");
assert.equal(projectsRedirectHref({ scope: "product:fixture-app", work: "work_a_held" }), "/apps/fixture-app");
assert.equal(projectsRedirectHref({ scope: "project:fixture-research", work: "work_a_held" }), "/work/work_a_held");
assert.equal(projectsRedirectHref({ scope: "project:fixture-research" }), "/work?lens=project");
assert.equal(projectsRedirectHref({ view: "map" }), "/work?lens=project");
assert.equal(projectsRedirectHref(new URLSearchParams("scope=__unlinked__")), "/work?lens=project");
assert.equal(projectsRedirectHref(null), "/work?lens=project");

console.log("work-list cases ok: filters, agent/app/group/search narrowing, stale oldest first, by-app links, retired Projects addresses");
