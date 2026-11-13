// Falsifiers for two wrong-data bugs:
// 1. the Work list labelled every item "Unassigned" when the executor join was
//    empty, although the work items carry owners;
// 2. Today, the sidebar and Work counted "waiting on you" three ways. There is
//    now one function (waitingOnYou) with named units, and unknown is null.
// Run: bun scripts/waiting-and-owner-smoke.mjs
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

console.log("waiting-and-owner-smoke ok: owners label lanes without an executor join; one waiting-on-you definition with named units");
