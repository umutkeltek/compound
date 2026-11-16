// Falsifiers for two wrong-data bugs:
// 1. the Work list labelled every item "Unassigned" when the executor join was
//    empty, although the work items carry owners;
// 2. Today, the sidebar and Work counted "waiting on you" three ways. There is
//    now one function (waitingOnYou) with named units, and unknown is null.
// Run: bun scripts/waiting-and-owner-cases.mjs. The wired gate runs it:
// work-legibility-smoke.mjs imports it, and a failing case fails that smoke.
import assert from "node:assert/strict";

const { buildWorkLensItems, groupWorkLensItems } = await import("../src/lib/work-views.ts");
const { waitingOnYou, toAttentionReport } = await import("../src/lib/attention.ts");

const emptyTraffic = { lanes: [], generatedAt: "", runningCount: 0, inFlightCount: 0, needsMeCount: 0, runAttemptsPresent: false, read: null };
const open = [
  { work_id: "work_aaaaaaaaaaaa", state: "running", claimed_by: "claude:lane-1", owner: "operator" },
  { work_id: "work_bbbbbbbbbbbb", state: "verifying", claimed_by: "codex:lane-2", owner: "operator" },
  { work_id: "work_cccccccccccc", state: "ready", owner: "claude" },
  { work_id: "work_dddddddddddd", state: "proposed" },
];
const items = buildWorkLensItems(open, emptyTraffic);
assert.deepEqual(items.map((i) => i.laneLabel), ["claude", "codex", "claude", "Unassigned"], "owners must label lanes when no executor row joins");
const groups = groupWorkLensItems(items, emptyTraffic);
assert.deepEqual(groups.map((g) => `${g.label}:${g.items.length}`), ["claude:2", "codex:1", "Unassigned:1"], "only an item with no owner is Unassigned");

// An executor lane that reports the item still wins over the owner.
const traffic = {
  ...emptyTraffic,
  lanes: [{ agentId: "exec-a", agentLabel: "Claude Code", agentStatus: "running", running: 1, needsMe: 0, inFlight: [{ workId: "work_aaaaaaaaaaaa", title: "", state: "running", nextAction: "", harness: "claude", runStatus: "running", actor: "x", retryDepth: 0, startedAt: "", lastHeartbeatAt: "" }] }],
};
const joined = buildWorkLensItems(open, traffic);
assert.equal(joined[0].laneLabel, "Claude Code");

// One definition of "waiting on you": groups vs items vs work items, named.
assert.equal(waitingOnYou(null), null, "an attention report that has not been read is unknown, not zero");
const report = toAttentionReport({
  schema_version: "compound.attention.v0.1",
  counts: { needs_you: 245, needs_you_groups: 22 },
  groups: [
    { key: "w", band: "needs_you", source: "workbench", count: 20, item_ids: [] },
    { key: "r1", band: "needs_you", source: "reviews", count: 200, item_ids: [] },
    { key: "r2", band: "needs_you", source: "reviews", count: 25, item_ids: [] },
    { key: "x", band: "watching", source: "reviews", count: 9, item_ids: [] },
  ],
});
const waiting = waitingOnYou(report);
assert.deepEqual(waiting, { groups: 22, items: 245, workItems: 20 });

// One stale rule (lib/work-state.ts): in progress and picked up more than
// STALE_AFTER_DAYS days before the clock that is passed in. The count on
// Work and Today uses the same predicate, so the two can never disagree.
{
  const state = await import("../src/lib/work-state.ts");
  const counts = await import("../src/lib/counts.ts");
  const NOW = Date.UTC(2031, 2, 10, 12, 0, 0);
  const DAY = 86_400_000;
  const ns = (ms) => `${BigInt(Math.round(ms)) * 1_000_000n}`;
  assert.equal(state.STALE_AFTER_DAYS, 7, "one threshold: seven days");
  assert.equal(counts.isStale, state.isStale, "counts.ts re-exports the one predicate, not a copy");
  assert.equal(counts.STALE_AFTER_DAYS, state.STALE_AFTER_DAYS);
  const cases = [
    { name: "running, picked up 8 days ago (ns string)", item: { work_id: "work_s1", state: "running", claimed_at: ns(NOW - 8 * DAY) }, stale: true },
    { name: "waiting for a check, picked up 30 days ago (ms)", item: { work_id: "work_s2", state: "verifying", claimed_at: NOW - 30 * DAY }, stale: true },
    { name: "picked up but not started, 9 days ago (ISO)", item: { work_id: "work_s3", state: "claimed", claimed_at: new Date(NOW - 9 * DAY).toISOString() }, stale: true },
    { name: "running, picked up exactly 7 days ago", item: { work_id: "work_s4", state: "running", claimed_at: ns(NOW - 7 * DAY) }, stale: false },
    { name: "running, picked up yesterday", item: { work_id: "work_s5", state: "running", claimed_at: ns(NOW - DAY) }, stale: false },
    { name: "held for 40 days is waiting, not in progress", item: { work_id: "work_s6", state: "held", claimed_at: ns(NOW - 40 * DAY) }, stale: false },
    { name: "ready, never picked up", item: { work_id: "work_s7", state: "ready" }, stale: false },
    { name: "running with no pick-up time: age unknown, not old", item: { work_id: "work_s8", state: "running" }, stale: false },
    { name: "running with an unreadable pick-up time", item: { work_id: "work_s9", state: "running", claimed_at: "soon" }, stale: false },
  ];
  for (const c of cases) assert.equal(state.isStale(c.item, NOW), c.stale, c.name);
  const items = cases.map((c) => c.item);
  assert.equal(
    counts.countInProgress(items, NOW).pickedUpOver7Days,
    items.filter((item) => state.isStale(item, NOW)).length,
    "the in-progress count's stale number is exactly the items the predicate calls stale",
  );
  // The clock is an argument: the same item is fresh at one time and stale later.
  const one = { state: "running", claimed_at: ns(NOW) };
  assert.equal(state.isStale(one, NOW + 7 * DAY), false);
  assert.equal(state.isStale(one, NOW + 7 * DAY + 1), true);
  // On hold and waiting-for-check share the same owner as the counts.
  assert.deepEqual(items.filter((i) => state.isOnHold(i.state)).map((i) => i.work_id), ["work_s6"]);
  assert.equal(counts.countOnHold([...items, { work_id: "work_s10", state: "blocked" }]), 2);
  assert.equal(state.isWaitingForCheck("verifying"), true);
  assert.equal(state.isWaitingForCheck("running"), false);
}

console.log("waiting-and-owner-cases ok: owners label lanes without an executor join; one waiting-on-you definition with named units; one stale rule shared by the count");
