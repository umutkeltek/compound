// Falsifiers for the work-to-project resolver (lib/work-project.ts). The
// grouping it replaces guessed identity from folder names and repository
// fingerprints and made several groups per app, and its tests read a field no
// real record carries. These cases use the shapes real records have: a
// `canonical_surface`, a worktree folder (`target_worktree_root` on workbench
// rows, `git_delivery.target_worktree_root` on `work list` rows), a catalog of
// apps with aliases, and inventory `code_in` folders. All names are invented.
// Run: bun scripts/work-project-cases.mjs. The wired gate runs it:
// work-legibility-smoke.mjs imports it, and a failing case fails that smoke.
import assert from "node:assert/strict";

// WORK_PROJECT_MODULE points the cases at another copy of the resolver, so a
// copy with one rule removed can be shown to fail the cases for that rule.
const modulePath = process.env.WORK_PROJECT_MODULE || "../src/lib/work-project.ts";
const {
  resolveProject, resolveProjects, buildWorkProjectIndex, compareProjects, catalogFromGrowth, inventoryFromJson,
  appGroupsFromWhere, foldAppGroups, NOT_LINKED_PROJECT_ID,
} = await import(modulePath);

const HOME = "/home/fixture-user";
const CODE = `${HOME}/Code`;
const LANES = "/srv/lanes";
const DAY = 86_400_000;
const NOW = Date.UTC(2031, 4, 20, 9, 0, 0);
const ns = (ms) => `${BigInt(Math.round(ms)) * 1_000_000n}`;

// The catalog and inventory as the projections print them.
const growth = {
  apps: [
    { product_id: "product:fixture-helper", name: "Fixture Helper", aliases: ["product:fixture_helper"] },
    { product_id: "product:honest-fixture", name: "Honest Fixture", aliases: ["product:fixture_tan"] },
    { product_id: "product:pocketlog", name: "Pocketlog", aliases: [] },
  ],
};
const inventoryJson = {
  resources: [
    { id: "product:fixture_helper", links: [
      { rel: "code_in", to: "repo:git.example/fixture-helper", evidence: "products/fixture-helper" },
      { rel: "code_in", to: "repo:git.example/fixture-helper", evidence: "~/Code/products/fixture-helper" },
      { rel: "deployed_on", to: "host:apps.example", evidence: "deploy log" },
    ] },
    { id: "product:fixture_tan", links: [{ rel: "code_in", to: "repo:git.example/tan", evidence: "products/tan-studio/app-swift" }] },
    { id: "product:pocketlog", links: [{ rel: "code_in", to: "repo:git.example/pocketlog", evidence: "products/pocketlog/mobile" }] },
    { id: "product:side-site", links: [{ rel: "code_in", to: "repo:git.example/side", evidence: "products/side-site" }] },
  ],
};
const catalog = catalogFromGrowth(growth);
const inventory = inventoryFromJson(inventoryJson);
const place = (work) => resolveProject(work, catalog, inventory);
const placeAll = (works) => resolveProjects(works, catalog, inventory);

const cases = [];
const test = (name, run) => cases.push({ name, run });

test("reads the catalog and inventory projections", () => {
  assert.equal(catalog.length, 3);
  assert.deepEqual(catalog[0], { productId: "product:fixture-helper", name: "Fixture Helper", aliases: ["product:fixture_helper"] });
  assert.deepEqual(inventory[0].codeIn, ["products/fixture-helper", "~/Code/products/fixture-helper"], "only code_in links");
  assert.equal(catalogFromGrowth({ generated_at: "x" }), null, "a payload with no app list is unknown, not empty");
  assert.equal(inventoryFromJson({}), null, "a payload with no resource list is unknown, not empty");
});

test("rule 1: hyphen, underscore and case twins of one product id are one app", () => {
  const ids = ["product:fixture-helper", "product:fixture_helper", "product:Fixture-Helper", "product:fixturehelper"]
    .map((surface) => place({ canonical_surface: surface }));
  for (const project of ids) assert.deepEqual(project, { id: "product:fixture-helper", name: "Fixture Helper", kind: "app", via: "product-id" });
});

test("rule 1: product:<app>:<release task> belongs to the app", () => {
  assert.deepEqual(place({ canonical_surface: "product:fixture_helper:release-42-notes-review" }), { id: "product:fixture-helper", name: "Fixture Helper", kind: "app", via: "product-id" });
});

test("rule 1: a product id the catalog does not list is not an app group", () => {
  const project = place({ canonical_surface: "product:not-in-catalog" });
  assert.equal(project.kind, "none");
  assert.equal(project.id, NOT_LINKED_PROJECT_ID);
});

test("rule 2: a worktree inside an app's code folder belongs to that app, whatever the folder is called", () => {
  const project = place({ target_worktree_root: `${CODE}/products/tan-studio/app-swift` });
  assert.deepEqual(project, { id: "product:honest-fixture", name: "Honest Fixture", kind: "app", via: "code-folder" });
  const deeper = place({ git_delivery: { target_worktree_root: `${CODE}/products/pocketlog/mobile/feature-x` } });
  assert.equal(deeper.id, "product:pocketlog", "the work list's git_delivery folder counts the same as the workbench field");
});

test("rule 3: folders `fixture-helper` and `build-7` under one fingerprint are both the app", () => {
  const works = [
    { work_id: "work_fx_1", repository_fingerprint: "fp-one", target_worktree_root: `${CODE}/products/fixture-helper` },
    { work_id: "work_fx_2", repository_fingerprint: "fp-one", target_worktree_root: `${LANES}/FixtureHelper/checkout-20300101/build-7` },
  ];
  const [a, b] = placeAll(works);
  assert.equal(a.id, "product:fixture-helper");
  assert.equal(a.via, "code-folder");
  assert.equal(b.id, "product:fixture-helper", "the lane folder name, not the deepest folder, names the app");
  assert.equal(b.via, "folder-name");
});

test("rule 3: a lane named after an app's code folder belongs to the app", () => {
  // `tan-studio` is neither the app's name nor an alias: only the inventory's code folder names it.
  assert.deepEqual(place({ target_worktree_root: `${LANES}/tan-studio/src-2.3.0` }), { id: "product:honest-fixture", name: "Honest Fixture", kind: "app", via: "folder-name" });
  assert.equal(place({ target_worktree_root: `${LANES}/pocketlog-release-9-20300101/app` }).id, "product:pocketlog");
});

test("rule 3: a task-name surface goes to the app it starts with, never becomes its own group", () => {
  assert.deepEqual(place({ canonical_surface: "pocketlog-export-date-fix" }), { id: "product:pocketlog", name: "Pocketlog", kind: "app", via: "surface-name" });
  for (const surface of ["-", "product", "fixture-cleanup-pass", "a free text task note"]) {
    assert.equal(place({ canonical_surface: surface }).kind, "none", `surface ${JSON.stringify(surface)} is not linked`);
  }
});

test("rule 4: a codebase is the folder directly under the projects group folder, never the deepest one", () => {
  const nested = place({ target_worktree_root: `${CODE}/systems/fixture-core/.agent/worktrees/deep-wt` });
  assert.deepEqual(nested, { id: "codebase:fixturecore", name: "fixture-core", kind: "codebase", via: "codebase-folder" });
  assert.equal(place({ target_worktree_root: `${CODE}/work/client-site/landing` }).id, "codebase:clientsite", "any group under the projects root");
  assert.equal(place({ target_worktree_root: `${CODE}/products/side-site` }).kind, "codebase", "an inventory product that is not a catalog app is a codebase");
});

test("two fingerprints, one folder: one group", () => {
  const index = buildWorkProjectIndex([
    { work_id: "work_fp_1", state: "ready", repository_fingerprint: "fp-old", target_worktree_root: `${CODE}/systems/fixture-core` },
    { work_id: "work_fp_2", state: "ready", repository_fingerprint: "fp-new", target_worktree_root: `${CODE}/systems/fixture-core` },
  ], catalog, inventory, NOW);
  assert.deepEqual(index.projects.map((p) => [p.id, p.open]), [["codebase:fixturecore", 2]]);
});

test("rule 5: a codebase found by folder also takes work that only names it", () => {
  const works = [
    { work_id: "work_cb_1", target_worktree_root: `${CODE}/systems/fixture-core` },
    { work_id: "work_cb_2", canonical_surface: "fixture-core:runtime-repair" },
    { work_id: "work_cb_3", canonical_surface: "system:fixture-core" },
    { work_id: "work_cb_4", target_worktree_root: `${LANES}/fixture-core-lint-pass-20300101` },
    { work_id: "work_cb_5", target_worktree_root: `${CODE}/systems/fixture-core-worktrees/wt-read` },
    { work_id: "work_cb_6", canonical_surface: "unrelated-topic" },
  ];
  const placed = placeAll(works).map((p) => p.id);
  assert.deepEqual(placed, ["codebase:fixturecore", "codebase:fixturecore", "codebase:fixturecore", "codebase:fixturecore", "codebase:fixturecore", NOT_LINKED_PROJECT_ID]);
  assert.equal(place(works[1]).kind, "none", "one record alone cannot know the codebase");
});

test("temp-folder work is never placed by its folder", () => {
  for (const root of ["/private/tmp/fixture-run.Ab12/products/app", "/tmp/fixture-bind-run/products/app", "/var/folders/xy/T/fixture/products/app"]) {
    const project = place({ target_worktree_root: root });
    assert.equal(project.kind, "none", `${root} is not a project`);
    assert.equal(project.via, "temp-folder");
  }
  assert.equal(placeAll([{ target_worktree_root: "/tmp/fixture-core-run" }, { target_worktree_root: `${CODE}/systems/fixture-core` }])[0].kind, "none", "a temp folder never joins a codebase by name");
  assert.equal(place({ target_worktree_root: "/tmp/run", canonical_surface: "product:pocketlog" }).id, "product:pocketlog", "a stated product still counts");
});

test("an operating-system home folder's owner name is never a project", () => {
  assert.equal(place({ target_worktree_root: "/Users/pocketlog/scratch/notes" }).kind, "none");
  assert.equal(place({ target_worktree_root: "/home/fixture-helper/scratch" }).kind, "none");
});

test("a name two apps both claim places nothing", () => {
  const twins = catalogFromGrowth({ apps: [
    { product_id: "product:alpha-fixture", name: "Alpha Fixture", aliases: ["product:shared-fixture"] },
    { product_id: "product:beta-fixture", name: "Beta Fixture", aliases: ["product:shared_fixture"] },
  ] });
  assert.equal(resolveProject({ canonical_surface: "product:shared-fixture" }, twins, []).kind, "none");
  assert.equal(resolveProject({ canonical_surface: "product:alpha-fixture" }, twins, []).id, "product:alpha-fixture");
});

test("rule 0: the record's own scope.product_id wins over surface and folder", () => {
  const work = {
    scope: { product_id: "product:Fixture_Helper" },
    canonical_surface: "product:pocketlog",
    target_worktree_root: `${CODE}/products/tan-studio/app-swift`,
  };
  assert.deepEqual(place(work), { id: "product:fixture-helper", name: "Fixture Helper", kind: "app", via: "scope" });
  assert.equal(place({ ...work, scope: {} }).id, "product:pocketlog", "an empty scope, as on today's records, falls through to the other rules");
});

test("rule 0: a recorded product the catalog does not list is its own group, never a guess from folders", () => {
  const work = { scope: { product_id: "product:side-tool" }, target_worktree_root: `${CODE}/products/fixture-helper` };
  assert.deepEqual(place(work), { id: "unlisted:sidetool", name: "side-tool", kind: "unlisted", via: "scope-unlisted" });
  assert.equal(place({ scope: { product_id: "product:side_tool" } }).id, "unlisted:sidetool", "its spellings still fold together");
  assert.equal(place({ scope: { product_id: "product:tan-studio" } }).kind, "unlisted", "a code folder's name is not a catalog id or alias");
  const index = buildWorkProjectIndex([{ work_id: "work_sc_1", state: "ready", ...work }], catalog, inventory, NOW);
  assert.deepEqual(index.projects.map((p) => [p.id, p.kind, p.open]), [["unlisted:sidetool", "unlisted", 1]]);
});

test("the identity owner's app groups: member spellings and folders join the catalog app, whose id stays", () => {
  const where = [
    { product_id: "product:fixture_helper", members: ["product:fixture_helper", "product:fixture-helper", "product:fxhelper"],
      surfaces: ["${HOME}/Code/products/helper-ios", "https://fixture-helper.example", "appstore:1234567", "file://${HOME}/Code/products/helper-web/README.md", "make verify"] },
    { product_id: "product:unknown-app", members: ["product:unknown-app"], surfaces: ["~/Code/products/unknown-app"] },
    { product_id: "product:both", members: ["product:pocketlog", "product:honest-fixture"], surfaces: ["~/Code/products/contested"] },
  ];
  const groups = appGroupsFromWhere(where);
  assert.equal(appGroupsFromWhere({ apps: [] }), null, "a payload that is not a list is unknown, not empty");
  const folded = foldAppGroups(catalog, inventory, groups);
  assert.deepEqual(folded.catalog.map((app) => app.productId), catalog.map((app) => app.productId), "the catalog's ids stay; no app is added");
  const helper = (work) => resolveProject(work, folded.catalog, folded.inventory);
  assert.deepEqual(helper({ canonical_surface: "product:fxhelper" }), { id: "product:fixture-helper", name: "Fixture Helper", kind: "app", via: "product-id" });
  assert.equal(place({ canonical_surface: "product:fxhelper" }).kind, "none", "without the groups that spelling is unknown");
  assert.deepEqual(helper({ target_worktree_root: `${CODE}/products/helper-ios/feature` }), { id: "product:fixture-helper", name: "Fixture Helper", kind: "app", via: "code-folder" });
  assert.equal(place({ target_worktree_root: `${CODE}/products/helper-ios/feature` }).kind, "codebase", "without the groups that folder is a codebase");
  assert.equal(helper({ canonical_surface: `${CODE}/products/helper-web/README.md` }).id, "product:fixture-helper", "a file surface counts as a folder");
  assert.equal(helper({ target_worktree_root: `${CODE}/products/unknown-app` }).kind, "codebase", "a group no catalog app matches adds nothing");
  assert.equal(helper({ target_worktree_root: `${CODE}/products/contested` }).kind, "codebase", "a group that matches two apps adds nothing");
  assert.equal(helper({ scope: { product_id: "product:fxhelper" } }).via, "scope", "a recorded spelling the identity owner lists folds into the app");
});

test("index: one group per app or codebase plus not linked, counts that add up", () => {
  const works = [
    { work_id: "work_ix_01", state: "held", canonical_surface: "product:fixture-helper" },
    { work_id: "work_ix_02", state: "running", claimed_at: ns(NOW - 10 * DAY), target_worktree_root: `${CODE}/products/fixture-helper` },
    { work_id: "work_ix_03", state: "verifying", claimed_at: ns(NOW - DAY), target_worktree_root: `${LANES}/FixtureHelper/x/build-7` },
    { work_id: "work_ix_04", state: "ready", canonical_surface: "product:fixture_helper" },
    { work_id: "work_ix_05", state: "claimed", claimed_at: ns(NOW - 2 * DAY), target_worktree_root: `${CODE}/products/tan-studio/app-swift` },
    { work_id: "work_ix_06", state: "verifying", claimed_at: ns(NOW - 40 * DAY), target_worktree_root: `${CODE}/systems/fixture-core` },
    { work_id: "work_ix_07", state: "blocked", canonical_surface: "fixture-core:repair" },
    { work_id: "work_ix_08", state: "proposed", canonical_surface: "-" },
    { work_id: "work_ix_09", state: "ready" },
    { work_id: "work_ix_10", state: "running", claimed_at: ns(NOW - 9 * DAY), target_worktree_root: "/tmp/run/products/app" },
    { work_id: "work_ix_10", state: "running", claimed_at: ns(NOW - 9 * DAY), target_worktree_root: "/tmp/run/products/app" },
  ];
  const index = buildWorkProjectIndex(works, catalog, inventory, NOW);
  assert.equal(Object.keys(index.byWork).length, 10, "a work id repeated in the list counts once");
  assert.equal(index.byWork.work_ix_03, "product:fixture-helper");
  const rows = Object.fromEntries(index.projects.map((p) => [p.id, [p.open, p.needYou, p.inProgress, p.waitingForCheck, p.stale]]));
  assert.deepEqual(rows, {
    "product:fixture-helper": [4, 1, 2, 1, 1],
    "product:honest-fixture": [1, 0, 1, 0, 0],
    "codebase:fixturecore": [2, 1, 1, 1, 1],
    [NOT_LINKED_PROJECT_ID]: [3, 0, 1, 0, 1],
  });
  assert.equal(index.projects.length, 4, "three surfaces of one app, a lane and a twin id make one group, not five");
  for (const field of ["open", "needYou", "inProgress", "waitingForCheck", "stale"]) {
    assert.equal(index.total[field], index.projects.reduce((sum, p) => sum + p[field], 0), `total ${field} is the sum over projects`);
  }
  assert.deepEqual(index.total, { open: 10, needYou: 2, inProgress: 5, waitingForCheck: 2, stale: 3 });
});

test("an unlinked group with held items sorts above a group with only waiting-for-check items", () => {
  const index = buildWorkProjectIndex([
    { work_id: "work_so_1", state: "verifying", canonical_surface: "product:pocketlog" },
    { work_id: "work_so_2", state: "verifying", canonical_surface: "product:pocketlog" },
    { work_id: "work_so_3", state: "verifying", canonical_surface: "product:pocketlog" },
    { work_id: "work_so_4", state: "held" },
  ], catalog, inventory, NOW);
  assert.deepEqual(index.projects.map((p) => p.id), [NOT_LINKED_PROJECT_ID, "product:pocketlog"]);
  const a = { id: "a", name: "A", kind: "app", open: 9, needYou: 0, inProgress: 9, waitingForCheck: 9, stale: 9 };
  const b = { id: "b", name: "B", kind: "none", open: 1, needYou: 1, inProgress: 0, waitingForCheck: 0, stale: 0 };
  assert.ok(compareProjects(b, a) < 0, "need you comes first");
});

const failures = [];
for (const { name, run } of cases) {
  try {
    run();
  } catch (error) {
    failures.push(`${name}: ${error instanceof Error ? error.message.split("\n")[0] : String(error)}`);
  }
}
if (failures.length) {
  console.error(`work-project cases FAILED (${failures.length} of ${cases.length})\n${failures.map((f) => `  - ${f}`).join("\n")}`);
  throw new Error("work-project cases failed");
}
console.log(`work-project cases ok: ${cases.length} cases (product-id twins, code folders, lane and task names, codebase folder not deepest folder, fingerprints ignored, codebase names, temp folders, home folders, index counts, order)`);
