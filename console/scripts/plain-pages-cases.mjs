// Plain-pages cases: render the real Today, Work, Decide and Live pages
// against a synthetic compound binary and check what a person reads.
//
//  1. No banned system phrase (lib/plain-words.ts BANNED_PHRASES) and no raw
//     area path used as a label appears in the visible text, while the reads
//     are loading and after they land.
//  2. Each page leads with its question, and the counts on every page come
//     from one definition (lib/counts.ts): the same fixture gives the same
//     "in progress" and "waiting" numbers on Today, Work, Decide and Live.
//  3. A read that has not landed shows its read state, never a zero.
//  4. Decide sends only the open decision's detail to the browser.
//
// Run as a child process by the wired work-legibility smoke (it owns its own
// scratch home and cache folder, and moves the read layer's global state, so
// it must not share a process with another smoke). Directly:
//   bun scripts/plain-pages-cases.mjs

import assert from "node:assert/strict";
import { chmod, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { AppRouterContext } from "next/dist/shared/lib/app-router-context.shared-runtime.js";
import { PathnameContext, SearchParamsContext } from "next/dist/shared/lib/hooks-client-context.shared-runtime.js";

const scratch = await mkdtemp(join(tmpdir(), "compound-plain-pages-"));
const bin = join(scratch, "compound-stub");
const releaseReads = join(scratch, "release-reads");

// Synthetic home: 6 open work items. In progress = 4 (1 running, 1 picked up,
// 2 waiting for a proof check); 2 of them picked up more than 7 days ago;
// 1 on hold; 7 decisions wait, 13 items in all.
await writeFile(bin, `#!/usr/bin/env bun
import { existsSync } from "node:fs";
const args = process.argv.slice(2).join(" ");
const now = Date.now();
const iso = (daysAgo) => new Date(now - daysAgo * 86400000).toISOString();
const ns = (daysAgo) => String(BigInt(Math.round(now - daysAgo * 86400000)) * 1000000n);
const work = [
  { work_id: "work_fx_check_old", state: "verifying", owner: "codex", claimed_by: "codex", claimed_at: ns(10), success_predicate: "Fixture check waits for its proof", next_action: "attach the final verified candidate" },
  { work_id: "work_fx_run_old", state: "running", owner: "codex", claimed_by: "codex", claimed_at: ns(9), success_predicate: "Fixture run is still going", next_action: "finish the run" },
  { work_id: "work_fx_claimed", state: "claimed", owner: "codex", claimed_by: "codex", claimed_at: ns(2), success_predicate: "Fixture item picked up recently", next_action: "start it" },
  { work_id: "work_fx_check_new", state: "verifying", owner: "claude_code", claimed_by: "claude_code:fixture-lane", claimed_at: ns(1), success_predicate: "Fixture check from yesterday", next_action: "attach the final verified candidate as commit:<full-sha>" },
  { work_id: "work_fx_held", state: "held", owner: "claude_code", claimed_by: "claude_code", claimed_at: ns(3), success_predicate: "Fixture item on hold", next_action: "resolve the hold", canonical_surface: "product:fixture-app" },
  { work_id: "work_fx_ready", state: "ready", owner: "codex", success_predicate: "Fixture item ready to start", next_action: "claim before touching" },
];
const group = (key, area, count, extra = {}) => ({
  key, area, band: "needs_you", source: "reviews", kind: "adjudicate", count,
  operator_summary: "A new " + area.split("/")[0].replace(/-/g, " ") + " record needs your decision.",
  operator_unlock: "Open it with compound reviews --area " + area + ".",
  blast_radius: "local_reversible", reversibility: "reversible", leverage: 1,
  oldest_created_at: iso(4), action: null,
  terminal_reason: "the owner has not exposed a safe action for this decision group",
  item_ids: Array.from({ length: count }, (_, i) => key + "-item-" + i),
  freshness: { generated_at: iso(0), source: "compound attention --json" },
  ...extra,
});
const groups = [
  group("workbench:blocked-on-you", "workbench-blocked-on-you", 1, {
    source: "workbench", kind: "gate_admit",
    operator_summary: "1 open work item is blocked on operator review.",
    operator_unlock: "Resolve the hold before executors can continue.",
    blast_radius: "destructive", reversibility: "irreversible",
    terminal_reason: "resolve the policy/source-control hold, then ready or cancel",
    item_ids: ["work_fx_held"],
  }),
  group("source-control-cleanup|scr", "source-control-cleanup/scr_0a1b2c3d4e5f.preimage", 3),
  group("launchd-repair|review", "launchd-repair", 4),
  group("git-index-lock|review", "git-index-lock", 2),
  group("bench|review", "bench", 1, { operator_summary: "product:fixture-app has a product hypothesis. Review it?", operator_unlock: "the product loop gets an evidence-shaped hypothesis" }),
  group("route-promotions|x", "route-promotions", 1, { terminal_reason: "actions are item-specific; no safe batch action exposed" }),
  group("worktree-retirement|review", "worktree-retirement", 1),
];
const watching = { ...group("route-promotions|watch", "route-promotions", 2), band: "watching", operator_summary: "Not enough evidence yet. Still watching." };
const reply = {
  "work list --json": { schema_version: "compound.work_list_projection.v0.1", work: work.map((w, i) => ({ ...w, id: w.work_id, scope: { project_id: i < 3 ? "fixture-research" : "fixture-operations" }, ids: { context_pack_id: "ctx_fixture_exact" } })) },
  "system inventory --kind product --json": { snapshot: { state: "fresh", observed_at: iso(0) }, resources: [{ id: "product:fixture-service", kind: "product", label: "Fixture service" }] },
  "system graph --kind product --json": { snapshot: { state: "fresh", observed_at: iso(0) }, edge_list: [] },
  "product growth --json": { generated_at: iso(0), apps: [{ product_id: "product:fixture-app", name: "Fixture App", aliases: [] }] },
  "attention --json": {
    schema_version: "compound.attention.v0.1", generated_at: iso(0),
    counts: { needs_you: 13, needs_you_groups: 7, actionable_now: 0, watching: 2, blocked_on_you: 1, fallback_groups: 0 },
    top: groups[0], groups: [...groups, watching],
    operator_attention: { schema_version: "compound.operator_attention.v0.1", revision: "r1", total: 0, items: [] },
  },
  "executors --json": {
    schema_version: "compound.executors_projection.v0.1", generated_at: iso(0),
    counts: { total: 4, open: 6, running: 4, ready: 1, needs_me: 1, assigned: 5, leases: 5 },
    delegated_sessions: [], delegated_counts: { total: 0, running: 0, exited: 0, refused: 0, unknown: 0 },
    executors: [
      { id: "codex", label: "Codex", status: "available", running_count: 3, assigned_count: 3, needs_me_count: 0, lease_count: 3,
        current_work: work.slice(0, 3).map((w) => ({ work_id: w.work_id, state: w.state, owner: w.owner, claimed_by: w.claimed_by, claimed_at: w.claimed_at, title: w.success_predicate, next_action: w.next_action })) },
      { id: "claude_code", label: "Claude Code", status: "manual", running_count: 1, assigned_count: 2, needs_me_count: 1, lease_count: 2,
        current_work: work.slice(3, 5).map((w) => ({ work_id: w.work_id, state: w.state, owner: w.owner, claimed_by: w.claimed_by, claimed_at: w.claimed_at, title: w.success_predicate, next_action: w.next_action })) },
      { id: "local_lm", label: "Local model", status: "not_checked", running_count: 0, assigned_count: 0, needs_me_count: 0, lease_count: 0, current_work: [] },
      { id: "unassigned", label: "Unassigned", status: "waiting", running_count: 0, assigned_count: 0, needs_me_count: 0, lease_count: 0, current_work: [] },
    ],
  },
  "workbench --json": { generated_at: iso(0), counts: { open: 6, in_progress: 4 }, open: work, recent: [] },
  "workbench --json --recent 500": { generated_at: iso(0), counts: { open: 6, in_progress: 4 }, open: work, recent: [] },
  "reviews --json": {
    schema_version: "compound.review_queue.v0.1", generated_at: iso(0), groups: [],
    items: [
      { id: "rev_fx_filed_1", area: "bench", band: "filed", status: "filed", operator_summary: "A filed bench record.", created_at: iso(5) },
      { id: "rev_fx_filed_2", area: "bench", band: "filed", status: "filed", operator_summary: "Another filed bench record.", created_at: iso(6) },
      { id: "rev_fx_filed_3", area: "source-control-cleanup/scr_99aa88bb.preimage", band: "filed", status: "filed", operator_summary: "A filed cleanup record.", created_at: iso(7) },
    ],
  },
  // The opened decision's own read (Decide ?decision=route-promotions|x): its
  // one item offers an action that carries no approval record.
  "reviews --json --area route-promotions": {
    schema_version: "compound.review_queue.v0.1", generated_at: iso(0), area: "route-promotions",
    groups: [{ key: "route-promotions|x", area: "route-promotions", band: "needs_you", item_ids: ["route-promotions|x-item-0"], terminal_reason: "actions are item-specific; no safe batch action exposed" }],
    items: [{ id: "route-promotions|x-item-0", area: "route-promotions", band: "needs_you", status: "review_required", operator_summary: "Promote the fixture route?", created_at: iso(4),
      actions: [{ id: "route_promotion_record", label: "Record the promotion", argv: ["compound", "route", "promotion-record", "--id", "fx"], requires_review: true }] }],
  },
  "policy operator-authority --json": { generated_at: iso(0), installed: true, autonomy_level: "L1", mode: "propose_only" },
  "source connections --json": { schema_version: "compound.source_connections_projection.v0.2", generated_at: iso(0), counts: {}, connections: [] },
  "director propose --dry-run --json": { generated_at: iso(0), proposals: [] },
  "packets list --json": { schema_version: "compound.work.offered_packets.v0.1", counts: {}, items: [] },
  "work leases --json": { leases: [] },
  // One item's own record, with the lifecycle list \`work show --json\` returns.
  "work show work_fx_held --json": {
    id: "work_fx_held", state: "held", owner: "claude_code", created_at: iso(12), success_predicate: "Fixture item on hold",
    canonical_surface: "product:fixture-app", evidence_refs: ["rec_fixture_one"],
    lifecycle: [
      { ts: ns(12), from: "", to: "ready", note: "created" },
      { ts: ns(11), from: "ready", to: "claimed", by: "claude_code:fixture-lane", note: "claimed" },
      { ts: ns(10), from: "claimed", to: "running", by: "claude_code:fixture-lane", note: "Started the fixture run." },
      { ts: ns(3), from: "running", to: "held", by: "claude_code:fixture-lane", note: "Waiting for a fixture device. (policy allow_with_obligations)" },
    ],
  },
  "work show work_fx_run_old --json": {
    id: "work_fx_run_old", state: "running", owner: "codex", created_at: iso(10), success_predicate: "Fixture run is still going",
    lifecycle: [
      { ts: ns(10), from: "", to: "ready", note: "created" },
      { ts: ns(9), from: "ready", to: "claimed", by: "codex", note: "claimed" },
      { ts: ns(9), from: "claimed", to: "running", by: "codex", note: "Started." },
    ],
  },
};
if (args === "work show work_fx_missing --json") { console.error("error: not found: /fixture-home/work/work_fx_missing.json"); process.exit(1); }
if (args === "version") { console.log("synthetic"); process.exit(0); }
// The test releases all reads only after every cold page has rendered.
while (!existsSync(${JSON.stringify(releaseReads)})) await Bun.sleep(5);
const key = Object.keys(reply).find((k) => args === k) ?? (args.startsWith("memory candidates") ? "memory" : "");
if (key === "memory") { console.log(JSON.stringify({ generated_at: iso(0), items: [], counts: { by_state: { promoted: 3, parked: 1, proposed: 1 } } })); process.exit(0); }
console.log(JSON.stringify(key ? reply[key] : { generated_at: iso(0) }));
`);
await chmod(bin, 0o755);
process.env.COMPOUND_BIN = bin;
process.env.COMPOUND_HOME = join(scratch, "home");
process.env.COMPOUND_CONSOLE_MUTATIONS = "0";
process.env.COMPOUND_CONSOLE_OPERATOR_SESSION = "0";
process.env.COMPOUND_CONSOLE_CACHE_DIR = join(scratch, "cache");
process.env.TMPDIR = scratch;

const { BANNED_PHRASES, findBannedPhrases } = await import("../src/lib/plain-words.ts");
const compound = await import("../src/lib/compound.ts");
const { default: Today } = await import("../src/app/page.tsx");
const { default: Work } = await import("../src/app/work/page.tsx");
const { default: Decide } = await import("../src/app/reviews/page.tsx");
const { default: Live } = await import("../src/app/live/page.tsx");
const { default: Projects } = await import("../src/app/projects/page.tsx");
const { default: WorkItem } = await import("../src/app/work/[id]/page.tsx");
const { GET: previewGET } = await import("../src/app/api/work/trace/route.ts");
const { buildDecideGroups } = await import("../src/lib/decide-model.ts");
const { agentNameFor, plainWhy, plainWorkStep, productTitle, standingLine } = await import("../src/lib/plain-words.ts");
const { humanizeProductId } = await import("../src/lib/app-identity.ts");

/**
 * A complete, current, allowed owner action for one review item (the
 * ActionIntent -> PolicyDecision -> AuthorityFace bundle the gate requires),
 * in an area that has a button (source-control-cleanup records a review).
 */
function authorityAction(itemId) {
  const scope = { entity_id: itemId, project_id: "fixture", surface: "reviews" };
  const target = { kind: "review_artifact", id: itemId };
  return {
    id: "record_source_control_cleanup_review",
    label: "Record the current source-control review",
    argv: ["compound", "authority", "dirty-review", `reviews/source-control-cleanup/${itemId}.review.yaml`, "--json"],
    requires_review: false,
    action_intent: {
      schema_version: "compound.action_intent.v0.1", id: `act_${itemId}`, created_at: "2026-07-17T12:00:00Z",
      ids: { work_id: "work_fixture_scoped" }, scope, capability: "source-control-review", target,
      mutation_class: "local_reversible", reversibility: "easy", blast_radius: "local_reversible",
      rollback_plan: "supersede the recorded review",
    },
    policy_decision: {
      schema_version: "compound.policy_decision.v0.1", id: `pol_${itemId}`, created_at: "2026-07-17T12:00:01Z",
      ids: { work_id: "work_fixture_scoped" }, scope, action_intent_id: `act_${itemId}`, outcome: "allow",
      obligations: [], standing_grant_id: "grant_fixture",
    },
    policy_authority: {
      schema_version: "compound.policy_authority_face.v0.1", revision: `policy-authority-v1:sha256:${"e".repeat(64)}`,
      current: true, allowed: true, wording: "Allowed by current policy", action_intent_id: `act_${itemId}`,
      policy_decision_id: `pol_${itemId}`, policy_outcome: "allow", standing_grant_id: "grant_fixture", target,
      target_scope: "private", blast_radius: "local_reversible", reversibility: "easy",
      verified_recoverability: "verified", operator_control_continuity: "continuous", policy_source: "fixture-policy",
      exact_gate: "standing_scope", expected_receipt: "source-control review receipt", expected_proof: ["proof:review-recorded"],
    },
  };
}

/**
 * Safety of the opened decision's own read (lib/decide-model.ts
 * ScopedDecisionEvidence): the gate still refuses evidence older than the
 * live window, and decision A's read never enables a button on decision B.
 */
async function scopedEvidenceCases() {
  const area = "source-control-cleanup";
  const item = (id) => ({ id, area, band: "needs_you", operator_summary: `Review ${id}.`, actions: [authorityAction(id)] });
  const group = (key, id) => ({ key, area, band: "needs_you", source: "reviews", count: 1, item_ids: [id], operator_summary: `Review ${id}.` });
  const listItems = new Map([["scr_a", item("scr_a")], ["scr_b", item("scr_b")]]);
  const groups = [group("scr|a", "scr_a"), group("scr|b", "scr_b")];
  const build = (listStatus, scoped) =>
    buildDecideGroups(groups, listItems, null, (g) => listItems.get(g.item_ids[0]), listStatus, scoped);
  const offered = (g) => g.face.state === "ready" || g.face.primaryAction != null || g.act.mode !== "copy";

  // Control: live evidence with a complete approval record shows the button.
  const live = build("live");
  assert.ok(live.every(offered), "control: live, complete evidence offers the action");
  assert.equal(standingLine(live[0].standing).text, "Ready for review");

  // 1. Evidence older than the live window: the gate refuses, whatever it found.
  const stale = build("stale");
  for (const g of stale) {
    assert.equal(offered(g), false, `${g.key}: no action from evidence older than the live window`);
    assert.equal(g.standing.kind, "ready", "the words still say what the last check found");
    assert.equal(g.standing.live, false);
    assert.doesNotMatch(standingLine(g.standing).text, /paused|You can decide this now/i);
  }
  // The opened decision's own read, when it is old too, is refused the same way.
  const ownItems = new Map([["scr_a", item("scr_a")], ["scr_b", item("scr_b")]]);
  const own = (status) => ({ key: "scr|a", status, group: groups[0], itemByID: ownItems, generatedAt: new Date().toISOString(), source: "compound reviews --json --area source-control-cleanup", checking: false });
  const staleOwn = build("stale", own("stale"));
  assert.equal(offered(staleOwn[0]), false, "a stale scoped read never offers the action");

  // 2. A's live read enables A only. B keeps the list's (stale) evidence even
  // though A's read carries a complete record for B too.
  const liveOwn = build("stale", own("live"));
  assert.equal(offered(liveOwn[0]), true, "A's own live read lets the gate offer A's action");
  assert.equal(liveOwn[0].act.mode === "generic" && liveOwn[0].act.itemId, "scr_a");
  assert.equal(offered(liveOwn[1]), false, "A's read never enables a button on B");
  assert.equal(liveOwn[1].standing.live, false);
  // A read keyed to another decision changes nothing here.
  const elsewhere = build("stale", { ...own("live"), key: "scr|other" });
  assert.ok(elsewhere.every((g) => !offered(g)), "a read for another decision enables nothing");
  // Still checking: the words say so, and nothing is offered.
  const checkingOwn = build("stale", { key: "scr|a", status: null, group: null, itemByID: null, generatedAt: "", source: "", checking: true });
  assert.equal(offered(checkingOwn[0]), false);
  assert.equal(standingLine(checkingOwn[0].standing).text, "Checking… (takes a few seconds)");
  assert.equal(checkingOwn[1].standing.checking, false, "only the opened decision says it is checking");
}

const router = Object.fromEntries(["push", "replace", "refresh", "back", "forward", "prefetch"].map((k) => [k, () => {}]));
const wrap = (path, element, query = "") =>
  renderToStaticMarkup(
    createElement(AppRouterContext.Provider, { value: router },
      createElement(PathnameContext.Provider, { value: path },
        createElement(SearchParamsContext.Provider, { value: new URLSearchParams(query) }, element))));

/** Render the Work page at an address: the server reads `searchParams`, the client views read the same query. */
async function renderWork(query) {
  const params = Object.fromEntries(new URLSearchParams(query));
  const html = wrap("/work", await Work({ searchParams: Promise.resolve(params) }), query);
  return { html, text: visibleText(html) };
}

/** The address a retired page redirects to (Next throws NEXT_REDIRECT with it in `digest`). */
async function redirectOf(render) {
  try {
    await render();
  } catch (error) {
    const match = /^NEXT_REDIRECT;[^;]*;([^;]+);/.exec(String(error?.digest ?? ""));
    if (match) return match[1];
    throw error;
  }
  throw new Error("expected a redirect");
}

const pages = {
  Today: { path: "/", render: () => Today(), question: "Today" },
  Work: { path: "/work", render: () => Work({ searchParams: Promise.resolve({}) }), question: "Work" },
  Decide: { path: "/reviews", render: () => Decide({ searchParams: Promise.resolve({}) }), question: "Decide" },
  Live: { path: "/live", render: () => Live(), question: "Live" },
};

/** What a person reads: text content only (no scripts, styles or attributes). */
function visibleText(html) {
  return html
    .replace(/<script[\s\S]*?<\/script>/g, " ")
    .replace(/<style[\s\S]*?<\/style>/g, " ")
    .replace(/<[^>]+>/g, " ")
    .replace(/&quot;/g, '"')
    .replace(/&#x27;|&#39;/g, "'")
    .replace(/&amp;/g, "&")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/\s+/g, " ");
}

async function renderAll() {
  const out = {};
  for (const [name, page] of Object.entries(pages)) {
    out[name] = visibleText(wrap(page.path, await page.render()));
  }
  return out;
}

function checkPlain(texts, when) {
  assert.ok(BANNED_PHRASES.length >= 15, "the banned list must not be emptied");
  for (const [name, text] of Object.entries(texts)) {
    const hits = findBannedPhrases(text);
    assert.deepEqual(hits, [], `${name} (${when}) shows system words: ${hits.join(", ")}`);
    assert.ok(text.includes(pages[name].question), `${name} (${when}) must name its page`);
  }
}

try {
  // The banned list catches what it claims to (a red check, not a silent one).
  assert.deepEqual(findBannedPhrases("Running · heartbeat-backed"), ["heartbeat-backed"]);
  assert.equal(findBannedPhrases("source-control-cleanup/scr_4b6d91fd142c.preimage").length, 1, "a raw area path is caught");

  // 1. Cold: nothing read yet. Every page says it is reading, never zero.
  // Today and Work share reads with Live. Hold fixture replies across both
  // render passes so a fast child cannot warm a later page's "cold" snapshot.
  for (const cold of [await renderAll(), await renderAll()]) {
    checkPlain(cold, "loading");
    assert.match(cold.Today, /Reading what waits for you for the first time/);
    assert.doesNotMatch(cold.Today, /Nothing needs you today|\b0 decisions\b/, "an unread attention copy is not 'nothing'");
    assert.match(cold.Work, /Reading work for the first time/);
    assert.doesNotMatch(cold.Work, /\b0 work items are marked in progress/);
    assert.match(cold.Decide, /Reading what waits for you for the first time/);
    assert.match(cold.Live, /Reading who works on what for the first time/);
  }
  assert.ok(compound.readQueueSnapshot().inflight > 0, "cold pages must exercise pending reads");

  // 2. Release the held replies, then prove that real populated reads land.
  await writeFile(releaseReads, "");
  await compound.settleProjectionReads();
  const warm = await renderAll();
  checkPlain(warm, "loaded");

  // Work grouped by app (lens=project): one row per app and the "not linked"
  // group, each count a link to exactly those items in that group.
  const byApp = await renderWork("lens=project");
  assert.deepEqual(findBannedPhrases(byApp.text), []);
  assert.match(byApp.text, /2 groups hold open work/);
  assert.match(byApp.text, /Fixture App App App page → 1 open work item 1 need you · 0 in progress · 0 waiting for a check · 0 not moved in a week/);
  assert.match(byApp.text, /Not linked to a project Names no app or codebase 5 open work items 0 need you · 4 in progress · 2 waiting for a check · 2 not moved in a week/);
  assert.ok(byApp.text.indexOf("Fixture App") < byApp.text.indexOf("Not linked to a project"), "the group that needs you comes first");
  assert.match(byApp.html, /href="\/work\?lens=table&amp;filter=blocked&amp;app=fixture-app"/, "need you opens that app's on-hold list");
  assert.match(byApp.html, /href="\/work\?lens=table&amp;filter=stale&amp;project=none"/, "not moved opens that group's stale list");
  assert.match(byApp.html, /href="\/apps\/fixture-app"/, "an app row links to its app page");
  // The List narrowed to one app shows only that app's work and names it.
  const appList = await renderWork("lens=table&filter=all&app=fixture-app");
  const appRows = appList.html.match(/<tbody>([\s\S]*?)<\/tbody>/)?.[1] ?? "";
  assert.match(appRows, /work_fx_held/);
  assert.doesNotMatch(appRows, /work_fx_check_old|work_fx_ready/, "another group's work is not in this app's list");
  assert.match(appList.text, /Only work for Fixture App ?\. Show all work/);
  // Every card and List row links to the item's own page.
  const board = await renderWork("");
  for (const id of ["work_fx_held", "work_fx_run_old", "work_fx_ready"]) {
    assert.match(board.html, new RegExp(`href="/work/${id}"`), `board card ${id} links to its page`);
  }
  const allList = await renderWork("lens=table&filter=all");
  assert.match(allList.html, /href="\/work\/work_fx_claimed"/, "a List row links to its page");

  // The item page: what it is, how old, what happened, what it is connected to.
  const held = visibleText(wrap("/work/work_fx_held", await WorkItem({ params: Promise.resolve({ id: "work_fx_held" }) })));
  const heldHTML = wrap("/work/work_fx_held", await WorkItem({ params: Promise.resolve({ id: "work_fx_held" }) }));
  assert.deepEqual(findBannedPhrases(held), []);
  assert.match(held, /Created \d{1,2} \w{3} \d{4} · Picked up \d{1,2} \w{3} \d{4} \(3 days ago\) · Last changed \d{1,2} \w{3} \d{4} \(3 days ago\)/, "dates under the title");
  assert.match(held, /Recorded reason On hold Waiting for a fixture device\. Nothing moves until the hold is lifted\./, "the recorded reason, without the policy suffix");
  assert.doesNotMatch(held, /The assigned agent needs to check|See next step|Work details/, "no generic line, no button that only scrolls");
  assert.match(held, /Held by Claude Code/, "the agent by its one name");
  assert.match(heldHTML, /href="\/apps\/fixture-app"/, "Related: its app page");
  assert.match(heldHTML, /href="\/work\?lens=table&amp;filter=all&amp;owner=claude_code"/, "Related: its agent's work");
  assert.match(heldHTML, /href="\/reviews\?decision=workbench%3Ablocked-on-you"/, "Related: the Decide entry that lists it");
  assert.match(heldHTML, /href="\/map\/lineage\/rec_fixture_one"/, "Related: evidence links to its history page");
  assert.match(held, /History Running → On hold by Claude Code/, "history, newest first, in plain words");
  assert.ok(held.indexOf("Running → On hold") < held.indexOf("Created as Ready to start"), "newest first");
  assert.match(held, /For agents: commands and technical record/, "commands sit behind a disclosure for agents");
  assert.ok(held.indexOf("Recorded reason") < held.indexOf("For agents"), "the reason comes before anything for agents");
  const stale = visibleText(wrap("/work/work_fx_run_old", await WorkItem({ params: Promise.resolve({ id: "work_fx_run_old" }) })));
  assert.match(stale, /In progress for more than 7 days without finishing\./, "a stale item says so");
  assert.doesNotMatch(held, /In progress for more than 7 days/, "an on-hold item is not stale");

  // The preview drawer's data: the same next step and dates; a failed read is a sentence, never the tool's error text.
  const preview = await (await previewGET(new Request("https://console.invalid/api/work/trace?id=work_fx_held"))).json();
  assert.equal(preview.preview.nextStep.reason, "Waiting for a fixture device.");
  assert.equal(preview.preview.nextStep.owner, "Claude Code");
  assert.equal(preview.preview.stateLabel, "On hold");
  assert.ok(preview.trace.lifecycle.length === 4, "the technical record carries the lifecycle from the JSON list");
  const missing = await previewGET(new Request("https://console.invalid/api/work/trace?id=work_fx_missing"));
  const missingBody = await missing.json();
  assert.equal(missing.status, 404);
  assert.doesNotMatch(JSON.stringify(missingBody), /fixture-home|\.json|not found:/, "no raw error text or path");

  // The retired Projects page redirects: an app scope to the app's page, else to Work by app.
  assert.equal(await redirectOf(() => Projects({ searchParams: Promise.resolve({ scope: "product:fixture-app" }) })), "/apps/fixture-app");
  assert.equal(await redirectOf(() => Projects({ searchParams: Promise.resolve({ scope: "project:fixture-research", work: "work_fx_check_old" }) })), "/work/work_fx_check_old");
  assert.equal(await redirectOf(() => Projects({ searchParams: Promise.resolve({}) })), "/work?lens=project");
  // One definition per number: the same fixture, the same numbers everywhere.
  for (const name of ["Today", "Live"]) {
    assert.match(warm[name], /4 work items are marked in progress/, `${name}: in progress uses the shared definition`);
  }
  for (const name of ["Today", "Live"]) {
    assert.match(warm[name], /1 running, 1 picked up but not started, 2 waiting for a proof check/, `${name}: in-progress split`);
    assert.match(warm[name], /2 of them were picked up more than 7 days ago/, `${name}: age from claimed_at`);
  }
  // Work leads with what is stopped, splits in progress into its named parts,
  // and every number opens the List of exactly those items.
  assert.match(warm.Work, /1 on hold, waiting for you · 2 not moved in over 7 days 4 in progress ?: 1 running · 1 picked up, not started · 2 waiting for a proof check 6 open work items in all\./);
  const header = await renderWork("");
  const rowsOf = (html) => (html.match(/<tbody>([\s\S]*?)<\/tbody>/)?.[1] ?? "").match(/<tr\b/g)?.length ?? 0;
  for (const [filter, n] of [["blocked", 1], ["stale", 2], ["progress", 4], ["running", 1], ["claimed", 1], ["verifying", 2], ["all", 6]]) {
    assert.match(header.html, new RegExp(`href="/work\\?lens=table&amp;filter=${filter}"`), `the header links ${filter}`);
    assert.equal(rowsOf((await renderWork(`lens=table&filter=${filter}`)).html), n, `the ${filter} list holds exactly the header's ${n}`);
  }
  assert.doesNotMatch(warm.Work, /wait for your decision|Open Decide/, "held work is one fact on Work, not two");
  // The List: as many cells per row as header cells (no shifted columns), the
  // tool's next step in plain words, no column that says nothing on every row.
  const listAll = await renderWork("lens=table&filter=all");
  const headCells = (listAll.html.match(/<thead>[\s\S]*?<\/thead>/)?.[0].match(/<th\b/g) ?? []).length;
  for (const tr of (listAll.html.match(/<tbody>([\s\S]*?)<\/tbody>/)?.[1] ?? "").match(/<tr\b[\s\S]*?<\/tr>/g) ?? []) {
    assert.equal((tr.match(/<td\b/g) ?? []).length, headCells, "each row has one cell per header");
  }
  assert.match(listAll.text, /Record the final checked commit for it\./, "Next in plain words, as on the board");
  assert.doesNotMatch(listAll.text, /commit:<full-sha>|Execution detail unavailable/);
  const css = (await import("node:fs")).readFileSync(new URL("../src/app/globals.css", import.meta.url), "utf8");
  assert.doesNotMatch(css, /\.work-focus-row::(before|after)/, "a generated box on a table row becomes an extra cell and shifts the columns");
  assert.match(warm.Today, /7 decisions wait for you, with 13 items in all/);
  assert.match(warm.Decide, /7 decisions waiting · 13 items to review/);
  assert.match(warm.Live, /1 work item waits for your decision/);
  assert.match(warm.Live, /2 agents hold 5 work items/, "agents count excludes the no-agent bucket and idle agents");
  // Work cards: the owner's agent named as Live names it, the lane id small;
  // the tool's next-step instruction in plain words.
  assert.match(warm.Work, /Claude Code claude_code:fixture-lane/, "a card names its agent, with the lane as small text");
  assert.match(warm.Work, /Record the final checked commit for it\./);
  assert.doesNotMatch(warm.Work, /commit:<full-sha>/, "a raw next-step instruction never reaches a card");

  // Today: at most five decisions, then one link to the rest; plain words for areas and reasons.
  assert.match(warm.Today, /2 more decisions on Decide/);
  assert.match(warm.Today, /Repository cleanup/, "area path becomes its plain kind");
  assert.match(warm.Today, /blocked on your review/, "system wording 'operator review' becomes 'your review'");
  assert.match(warm.Today, /Why: Agents cannot go on until you lift the hold\./, "a known 'why' line reads in plain words");
  assert.doesNotMatch(warm.Today, /executors can continue/, "system wording never reaches the why line");
  // This week: the autonomy level in one plain sentence, the level itself small.
  assert.match(warm.Today, /Autonomy L1 Agents suggest changes and ask you before they change files on this machine\./);
  assert.doesNotMatch(warm.Today, /propose only|self-authorizes/);
  // A title that starts with a product id leads with the product's name; the id is small text.
  const titled = productTitle("product:fixture-app has a product hypothesis.", humanizeProductId);
  assert.deepEqual(titled, { text: "Fixture App has a product hypothesis.", id: "product:fixture-app" });
  assert.deepEqual(productTitle("A plain title.", humanizeProductId), { text: "A plain title.", id: "" });
  // Unknown tool text is shown as written, never hidden.
  assert.equal(plainWorkStep("an instruction nobody mapped"), "an instruction nobody mapped");
  assert.equal(plainWhy("a reason nobody mapped"), "A reason nobody mapped.");
  assert.deepEqual(agentNameFor("claude_code:lane-x", new Map([["claude_code", "Claude Code"]])), { name: "Claude Code", lane: "claude_code:lane-x" });
  assert.deepEqual(agentNameFor("someone:else", new Map([["claude_code", "Claude Code"]])), { name: "someone:else", lane: "" });
  // Other spellings of a known agent find its label through the one agent key.
  assert.deepEqual(agentNameFor("harness:fixture-agent", new Map([["fixture_agent", "Fixture Studio"]])), { name: "Fixture Studio", lane: "harness:fixture-agent" });
  assert.deepEqual(agentNameFor("Fixture-Agent:lane-2", new Map([["fixture_agent", "Fixture Studio"]])), { name: "Fixture Studio", lane: "Fixture-Agent:lane-2" });
  assert.match(warm.Today, /There is no button for this kind of decision yet/);
  assert.doesNotMatch(warm.Today, /compound reviews --area/, "a command is never the 'why'");

  // Decide: filed is a count plus kinds, not one chip per item; reasons in plain words.
  assert.match(warm.Decide, /Filed .*3 items/);
  assert.match(warm.Decide, /Benchmark results/);
  // Where each decision stands, from the data: here every decision's kind has
  // no button, so it is said once above the list, and the answer counts it.
  assert.match(warm.Decide, /Why none can be decided now/);
  assert.match(warm.Decide, /Needs follow-up before it can be applied/);
  assert.match(warm.Decide, /7 have no button for their kind of decision yet\./);
  assert.doesNotMatch(warm.Decide, /paused|pause while/i, "an old copy is only old: never 'paused'");
  // A title that starts with a product id leads with the product's name on
  // Decide too, as on Today; the id stays as small text.
  assert.match(warm.Decide, /Fixture App has a product hypothesis\. Review it\?/);
  assert.match(warm.Today, /Fixture App has a product hypothesis\. Review it\?/);
  assert.doesNotMatch(warm.Decide, /product:fixture-app has/, "a raw product id never leads a Decide title");

  // 5. The opened decision checks its own evidence (`reviews --json --area`):
  // "Checking…" while that read runs, then its real standing from that read.
  const openedPath = { decision: "route-promotions|x" };
  const renderOpened = async () => visibleText(wrap("/reviews", await Decide({ searchParams: Promise.resolve(openedPath) })));
  const checking = await renderOpened();
  assert.match(checking, /Checking… \(takes a few seconds\)/, "the opened decision says it is being checked");
  await compound.settleProjectionReads();
  const opened = await renderOpened();
  assert.doesNotMatch(opened, /Checking…/);
  assert.match(
    opened,
    /Each item has its own action\. Open it to decide them one by one\./,
    "the opened decision shows its real standing from its own read",
  );
  assert.doesNotMatch(opened, /approval record is incomplete: each item/, "one action per item is not an incomplete record");
  // The answer counts it apart: no button / incomplete record / item by item.
  assert.match(opened, /6 have no button for their kind of decision yet and 1 is decided item by item/);
  assert.deepEqual(findBannedPhrases(opened), []);
  await scopedEvidenceCases();

  // 6. The opened card: product name in the title, the id small, and the
  // tool's "why" line in plain words in its "Why it matters" box.
  const bench = visibleText(wrap("/reviews", await Decide({ searchParams: Promise.resolve({ decision: "bench|review" }) })));
  assert.match(bench, /Fixture App has a product hypothesis\. Review it\?/);
  assert.match(bench, /product:fixture-app/, "the id stays, as small text");
  assert.match(bench, /The product gets an idea to test that rests on evidence\./);
  assert.doesNotMatch(bench, /evidence-shaped/, "the tool's raw why line never reaches the card");

  // 4. Decide sends only the open decision's detail.
  const page = await Decide({ searchParams: Promise.resolve({}) });
  const queues = [];
  const visit = (node) => {
    if (Array.isArray(node)) return node.forEach(visit);
    if (!node || typeof node !== "object") return;
    if (node.type?.name === "DecideQueue") queues.push(node.props);
    visit(node.props?.children);
  };
  visit(page);
  assert.equal(queues.length, 1, "Decide renders one queue");
  const [queue] = queues;
  assert.equal(queue.groups.length, 7, "every decision is listed");
  assert.deepEqual(queue.detailKeys, [queue.groups[0].key], "only the open decision carries detail");
  for (const group of queue.groups.slice(1)) {
    assert.equal(group.members.length, 0, `${group.key}: list-only rows carry no members`);
    assert.equal(group.evidence.length, 0, `${group.key}: list-only rows carry no evidence`);
  }

  console.log(`plain-pages cases ok: 4 pages × 2 read states, Work by app and the retired Projects redirect, free of ${BANNED_PHRASES.length} banned phrases; shared counts agree; Decide sends one decision's detail; opened decision checks its own read (stale refused, A's read never enables B)`);
} finally {
  await writeFile(releaseReads, "");
  await compound.settleProjectionReads();
  await rm(scratch, { recursive: true, force: true });
}
