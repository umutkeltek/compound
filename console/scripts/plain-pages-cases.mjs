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
// 1 on hold; 6 decisions are his (12 items), 1 more waits for wording, 2 skill proposals are housekeeping.
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
  group("worktree-retirement|review", "worktree-retirement", 1, { needs_wording: true }),
  // Proposals to update agent skills: agent housekeeping, never counted as his.
  group("capability-lifecycle-proposals|clp_a", "capability-lifecycle-proposals", 1, { operator_summary: "Review 12 recent fixture hook events." }),
  group("capability-lifecycle-proposals|clp_b", "capability-lifecycle-proposals", 1, { operator_summary: "Review 30 recent fixture hook events." }),
];
const watching = { ...group("route-promotions|watch", "route-promotions", 2), band: "watching", operator_summary: "Not enough evidence yet. Still watching." };
const othersWait = { ...group("workbench:waiting-on-others", "workbench-waiting-on-others", 1), band: "watching", source: "workbench", kind: "waiting_on_others", item_ids: ["work_fx_ready"] };
const reply = {
  "work list --json": { schema_version: "compound.work_list_projection.v0.1", work: work.map((w, i) => ({ ...w, id: w.work_id, scope: { project_id: i < 3 ? "fixture-research" : "fixture-operations" }, ids: { context_pack_id: "ctx_fixture_exact" } })) },
  "system inventory --kind product --json": { snapshot: { state: "fresh", observed_at: iso(0) }, resources: [{ id: "product:fixture-service", kind: "product", label: "Fixture service" }] },
  "system graph --kind product --json": { snapshot: { state: "fresh", observed_at: iso(0) }, edge_list: [] },
  "product growth --json": { generated_at: iso(0), apps: [{ product_id: "product:fixture-app", name: "Fixture App", aliases: [] }] },
  "attention --json": {
    schema_version: "compound.attention.v0.1", generated_at: iso(0),
    counts: { needs_you: 15, needs_you_groups: 9, actionable_now: 0, watching: 3, blocked_on_you: 1, fallback_groups: 0 },
    top: groups[0], groups: [...groups, watching, othersWait],
    totals: {
      total_read: 25, live_decisions: 15, set_aside: 9, closed_without_you: 9, waiting_on_others: 1,
      set_aside_reasons: [{ reason: "subject_gone", reason_text: "What it is about no longer exists.", count: 9, closed: true, next_step: "Nothing is left for you." }],
    },
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
  "work show work_fx_held --json": { ...work.find((w) => w.work_id === "work_fx_held"), id: "work_fx_held", lifecycle: [{ to: "claimed", ts: ns(5), note: "claimed" }, { to: "held", ts: ns(3), note: "Waits for a fixture account. (policy fixture)" }] },
  "workbench --json --recent 500": { generated_at: iso(0), counts: { open: 6, in_progress: 4 }, open: work, recent: [] },
  "reviews --json": {
    schema_version: "compound.review_queue.v0.1", generated_at: iso(0), groups: [],
    items: [
      { id: "rev_fx_filed_1", area: "bench", band: "filed", status: "filed", operator_summary: "A filed bench record.", created_at: iso(5) },
      { id: "rev_fx_filed_2", area: "bench", band: "filed", status: "resolved", operator_summary: "Another filed bench record. Review it?", created_at: iso(6), resolution: { decision: "dismissed", actor: "operator:fixture", resolved_at: iso(2) } },
      { id: "rev_fx_filed_3", area: "source-control-cleanup/scr_99aa88bb.preimage", band: "filed", status: "completed", operator_summary: "A filed cleanup record.", next_action: "cleanup proposal rejected and filed", created_at: iso(7) },
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
  // The page names come from lib/experience.ts (Decide is "All decisions", Live is "Working now").
  Decide: { path: "/reviews", render: () => Decide({ searchParams: Promise.resolve({}) }), question: "All decisions" },
  Live: { path: "/live", render: () => Live(), question: "Working now" },
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
  assert.doesNotMatch(held, /Review blocked work|Follow progress and resolve/, "the Work list's button and lead are not the item's");
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
  // One name per state: the List's badge uses the board's words.
  assert.match(listAll.html, />On hold</, "the List says On hold, as the board does");
  assert.doesNotMatch(listAll.html, />(held|verifying|claimed)</, "no raw state word in a badge");
  // On hold is the first column on the board when it has items.
  const columns = [...header.html.matchAll(/<section[^>]*aria-label="([^"]+)"/g)].map((m) => m[1]);
  assert.equal(columns[0], "On hold", `on hold leads the board: ${columns.join(", ")}`);
  // Retired views open the board; Work shows three views and no activity section.
  for (const lens of ["pipeline", "flow", "space", "offered"]) {
    const retired = await renderWork(`lens=${lens}`);
    assert.match(retired.html, /aria-label="On hold"/, `lens=${lens} opens the board`);
  }
  assert.doesNotMatch(header.text, /Stages|By agent|Work packets|Recent activity/, "the retired views and the activity section left Work");
  // The board's search and agent filter come from the address; a search with no match says so and offers a way out.
  const noMatch = await renderWork("q=zz-nothing-matches");
  assert.match(noMatch.text, /No work matches .zz-nothing-matches.\. Clear search/);
  assert.match(noMatch.text, /No match in this column\./);
  assert.match(noMatch.text, /0 of 6 open match/);
  const byAgent = await renderWork("owner=claude-code");
  assert.match(byAgent.text, /2 of 6 open match/, "the agent filter keeps that agent's cards in any spelling");
  assert.match(header.text, /6 open(?! match)/, "one total, named");
  // A card names its app and its agent on one line.
  assert.match(header.text, /F Fixture App · Claude Code Fixture item on hold/);
  const css = (await import("node:fs")).readFileSync(new URL("../src/app/globals.css", import.meta.url), "utf8");
  assert.doesNotMatch(css, /\.work-focus-row::(before|after)/, "a generated box on a table row becomes an extra cell and shifts the columns");
  // One count, only what is his: Today's sentence, Decide's header and the
  // sidebar badge say the same number under the same name. Skill proposals,
  // an unworded record, what closed without him and what waits on others are
  // named apart and never added in.
  {
    // The sidebar badge is NavContainer's `waitingOnYou(readAttention().report).groups`.
    const { readAttention, waitingOnYou } = await import("../src/lib/attention.ts");
    const sidebar = waitingOnYou((await readAttention()).report);
    assert.equal(sidebar.groups, 6, "the sidebar badge counts only his six decisions");
    for (const name of ["Today", "Decide"]) {
      assert.match(warm[name], /6 decisions wait for you ?\./, `${name}: the one waiting count`);
      assert.doesNotMatch(warm[name], /\b(9|15) decisions\b|13 items|15 items/, `${name}: no second count`);
      assert.match(warm[name], /1 more decision is yours once an agent words it as a clear question\./, `${name}: the unworded record is named apart`);
      assert.match(warm[name], /9 records closed without you: nothing is left to decide\./, `${name}: closed without you`);
      assert.match(warm[name], /1 work item waits on something outside you, not on you\./, `${name}: waiting on others`);
      assert.match(warm[name], /2 proposals to update agent skills are agent housekeeping, not decisions for you, so they are not counted\./, `${name}: skill proposals folded out`);
      assert.equal(warm[name].split("To decide, tell an agent in chat.").length - 1, 1, `${name}: how a decision moves is said once`);
      assert.doesNotMatch(warm[name], /Connect operator|Check console access/, `${name}: no way to decide that does not exist`);
    }
    assert.match(warm.Decide, /What it is about no longer exists\./, "Decide opens the reasons for what closed without him");
    assert.doesNotMatch(warm.Decide, /fixture hook events/, "a skill proposal is never a card on Decide");
    assert.doesNotMatch(warm.Today, /fixture hook events/, "a skill proposal is never a row on Today");
  }
  assert.match(warm.Live, /1 work item waits for your decision/);
  assert.match(warm.Live, /2 agents hold 5 work items/, "agents count excludes the no-agent bucket and idle agents");
  // Work cards: the owner's agent named as Live names it, the lane id small;
  // the tool's next-step instruction in plain words.
  assert.match(warm.Work, /Claude Code claude_code:fixture-lane/, "a card names its agent, with the lane as small text");
  assert.match(warm.Work, /Record the final checked commit for it\./);
  assert.doesNotMatch(warm.Work, /commit:<full-sha>/, "a raw next-step instruction never reaches a card");

  // Today: at most five decisions, then one link to the rest; plain words for areas and reasons.
  assert.match(warm.Today, /1 more decision on Decide/);
  assert.match(warm.Today, /Repository cleanup/, "area path becomes its plain kind");
  assert.match(warm.Today, /blocked on your review/, "system wording 'operator review' becomes 'your review'");
  assert.doesNotMatch(warm.Today, /Why: /, "the row asks the question instead of a second 'why' wording");
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
  assert.match(warm.Today, /Let this held work continue, or stop it\?/, "a row asks its kind's question, as Decide's card does");
  assert.doesNotMatch(warm.Today, /compound reviews --area/, "a command is never the 'why'");

  // Decide: filed is a count plus kinds, not one chip per item; reasons in plain words.
  assert.match(warm.Decide, /Filed .*3 items/);
  assert.match(warm.Decide, /Benchmark results/);
  // Where each decision stands, from the data: here every decision's kind has
  // no button, so it is said once above the list, and the answer counts it.
  assert.doesNotMatch(warm.Decide, /Why none can be decided now|Ready to review|Needs a closer look/, "one still list: no readiness sections and no banner");
  assert.doesNotMatch(warm.Decide, /Needs follow-up before it can be applied|Not yet/, "no gate state on rows: how a decision moves is said once");
  assert.doesNotMatch(warm.Decide, /Recording actions is unavailable|Queue status/, "the buried queue-status note is gone: how a decision moves is said once, up top");
  assert.doesNotMatch(warm.Decide, /paused|pause while/i, "an old copy is only old: never 'paused'");
  // A title that starts with a product id leads with the product's name on
  // Decide too, as on Today; the id stays as small text.
  assert.match(warm.Decide, /Fixture App has a product hypothesis\./);
  assert.match(warm.Today, /Fixture App has a product hypothesis\./);
  // Today and Decide agree: the same rows in the same order, each with the
  // same title, app and wait (one function, decide-model decisionHeadline).
  {
    const rowsOf = (text) => [...text.matchAll(/waiting (\d+ (?:day|hour|minute)s?)/g)].map((m) => m[1]);
    const todayRows = rowsOf(warm.Today.slice(warm.Today.indexOf("Start with the first one"), warm.Today.indexOf("more decision on Decide")));
    const decideRows = rowsOf(warm.Decide.slice(warm.Decide.indexOf("Filter decisions") >= 0 ? warm.Decide.indexOf("Filter decisions") : 0));
    assert.ok(todayRows.length >= 5, `Today shows its rows with their wait: ${todayRows.join(", ")}`);
    assert.deepEqual(decideRows.slice(0, todayRows.length), todayRows, "Today's rows wait as long as Decide's top rows say");
    for (const title of ["1 open work item is blocked on your review.", "Fixture App has a product hypothesis."]) {
      assert.ok(warm.Today.includes(title) && warm.Decide.includes(title), `one title on both pages: ${title}`);
    }
    assert.doesNotMatch(warm.Today, /cannot be undone|Deletes or destroys/, "no aggregate destructive label on a row");
    assert.doesNotMatch(warm.Today, /There is no button for this kind of decision yet/, "no dead-end reason on every row: how a decision moves is said once");
  }
  assert.doesNotMatch(warm.Decide, /product:fixture-app has/, "a raw product id never leads a Decide title");

  // 5. The opened decision is a card, never a "checking" gate: no button
  // waits on a recheck, so nothing on it changes when one runs.
  const opened = visibleText(wrap("/reviews", await Decide({ searchParams: Promise.resolve({ decision: "route-promotions|x" }) })));
  assert.doesNotMatch(opened, /Checking…|Not yet|Ready to review/);
  assert.deepEqual(findBannedPhrases(opened), []);
  await compound.settleProjectionReads();
  await scopedEvidenceCases();
  await scopedEvidenceCases();

  // 6. One card per decision: the app it is about (linked), the question in
  // one sentence, what a yes and a no would cause and whether it can be
  // undone, from the kind's words; what the record does not say, once.
  const route = visibleText(wrap("/reviews", await Decide({ searchParams: Promise.resolve({ decision: "route-promotions|x" }) })));
  assert.match(route, /Let agents use this model first for this kind of task\?/, "the kind's question");
  assert.match(route, /If you say yes Agents try this model first for this kind of task\./);
  assert.match(route, /If you say no Nothing changes/);
  assert.match(route, /Can it be undone\? Yes: the first choice can be switched back\./);
  assert.match(route, /The record does not say what is recommended\./, "what the record does not say, named once");
  assert.doesNotMatch(route, /No recommendation is recorded|The system does not say|Connect operator|Copy task|Next step/, "no boilerplate and no dead control");
  const bench = await Decide({ searchParams: Promise.resolve({ decision: "bench|review" }) });
  const benchText = visibleText(wrap("/reviews", bench));
  assert.match(benchText, /App Fixture App/, "the card names the app it is about");
  assert.match(wrap("/reviews", bench), /href="\/apps\/fixture-app"/, "and links its page");
  assert.match(benchText, /The record does not say what it asks, what a yes does, what a no does, whether it can be undone or what is recommended\./);
  // Held work: each item inline with its title, app, recorded reason and since when.
  const heldCold = await Decide({ searchParams: Promise.resolve({ decision: "workbench:blocked-on-you" }) });
  await compound.settleProjectionReads();
  const heldNow = await Decide({ searchParams: Promise.resolve({ decision: "workbench:blocked-on-you" }) });
  const heldText = visibleText(wrap("/reviews", heldNow));
  assert.ok(heldCold, "the held decision renders before its records are read");
  assert.match(heldText, /Let this held work continue, or stop it\?/);
  assert.match(heldText, /Fixture item on hold →/, "the held item by its title");
  assert.match(heldText, /Fixture App · Waiting for a fixture device\./, "its app and the recorded reason (blocking_reason first, as on the work page)");
  assert.match(heldText, /On hold since 3d ago/);
  assert.match(wrap("/reviews", heldNow), /href="\/work\/work_fx_held"/, "it links the work page");
  assert.doesNotMatch(heldText, /Deletes or destroys something/, "no aggregate destructive label");

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

  // The queue holds still: one list in the one shared order. A decision whose
  // check found something else (here: one turned "ready") stays where it was;
  // no section regroups it and no banner appears.
  {
    const { DecideQueue } = await import("../src/components/decide/DecideQueue.tsx");
    const flipped = queue.groups.map((group, index) => (index === 3 ? { ...group, face: { ...group.face, state: "ready" } } : group));
    const html = wrap("/reviews", createElement(DecideQueue, { ...queue, groups: flipped }), `decision=${encodeURIComponent(queue.groups[0].key)}`);
    const order = [...html.matchAll(/data-decision-row="([^"]+)"/g)].map((m) => m[1].replaceAll("&amp;", "&"));
    assert.deepEqual(order, queue.groups.map((group) => group.key), "a recheck never reorders or regroups the list");
    assert.doesNotMatch(html, /Ready to review|Needs a closer look|Why none can be decided now|cannot be decided now/);
  }
  // Search text and kind come from the address (`q`, `area`), so a reload or
  // a pasted link keeps them, and search matches only what a row shows.
  {
    const { DecideQueue } = await import("../src/components/decide/DecideQueue.tsx");
    const rowsAt = (query) => [...wrap("/reviews", createElement(DecideQueue, queue), query).matchAll(/data-decision-row="([^"]+)"/g)].map((m) => m[1]);
    assert.deepEqual(rowsAt("q=hypothesis"), ["bench|review"], "q filters to the rows that show the word");
    assert.deepEqual(rowsAt("area=launchd-repair"), ["launchd-repair|review"], "area filters to one kind");
    assert.deepEqual(rowsAt("q=operator"), [], "search never matches hidden tool text (the row says 'your review')");
    assert.match(wrap("/reviews", createElement(DecideQueue, queue), "q=hypothesis"), /value="hypothesis"/, "the search box shows the address's text");
    const { decideFilterHref } = await import("../src/components/decide/DecideQueue.tsx");
    assert.equal(decideFilterHref("/reviews", "decision=a&filed=Sources", { q: "hold", area: "" }), "/reviews?decision=a&filed=Sources&q=hold", "typing keeps the open decision and other parameters");
  }

  // What happened: filed items lead with their outcome and when it was
  // decided; recent decisions read across kinds; watching rows never claim to
  // wait on him under a heading that says they do not, and lead somewhere.
  {
    const filed = visibleText(wrap("/reviews", await Decide({ searchParams: Promise.resolve({ filed: "Repository cleanup" }) }), "filed=Repository+cleanup"));
    assert.match(filed, /Recent decisions, newest first Dismissed · Another filed bench record\. Benchmark results · decided 2d ago by you/, "a recent decision leads with its outcome, date and who");
    assert.match(filed, /Cleanup proposal rejected and filed · A filed cleanup record\. Repository cleanup · raised 7d ago/, "a filed row leads with the outcome the record carries");
    assert.doesNotMatch(filed, /until you decide/);
  }

  // A link to a decision that is gone says so and offers the filed list; a
  // skill-proposal link says it is agent housekeeping. Neither opens another
  // decision silently.
  for (const [key, said] of [
    ["route-promotions|gone", /That decision is no longer waiting for you: it was answered or closed\. See filed items →/],
    ["capability-lifecycle-proposals|clp_a", /That link opens a proposal to update agent skills\. It is agent housekeeping, not a decision for you/],
  ]) {
    const html = wrap("/reviews", await Decide({ searchParams: Promise.resolve({ decision: key }) }), `decision=${encodeURIComponent(key)}`);
    assert.match(visibleText(html), said, key);
    assert.match(html, /href="#filed"/, `${key}: offers the filed list`);
  }

  console.log(`plain-pages cases ok: 4 pages × 2 read states, Work by app and the retired Projects redirect, free of ${BANNED_PHRASES.length} banned phrases; shared counts agree; Decide sends one decision's detail; opened decision checks its own read (stale refused, A's read never enables B)`);
} finally {
  await writeFile(releaseReads, "");
  await compound.settleProjectionReads();
  await rm(scratch, { recursive: true, force: true });
}
