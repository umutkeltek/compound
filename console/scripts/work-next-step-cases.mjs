import assert from "node:assert/strict";
import { workNextStep } from "../src/lib/work-next-step.ts";

const work = {
  state: "held", owner: "fixture-agent",
  lifecycle: [
    { from: "running", to: "held", note: "Waiting for a test device. (policy allow_with_obligations)" },
    { from: "held", to: "held", note: "Waiting for the second test account. (policy allow_with_obligations)" },
    { from: "held", to: "held", note: "action recorded: inspect_task" },
  ],
};
assert.equal(workNextStep(work).reason, "Waiting for the second test account.");
assert.equal(workNextStep(work).owner, "Fixture Agent", "the owner by the agent's one name, never the raw id");
assert.equal(workNextStep({ ...work, claimed_by: "fixture_agent:lane-2" }).owner, "Fixture Agent");
assert.equal(workNextStep({ ...work, blocking_reason: "Current dependency" }).reason, "Current dependency");
assert.doesNotMatch(workNextStep({ ...work, state: "ready" }).reason, /test account|test device/);
assert.match(workNextStep({ state: "held" }).reason, /No reason was recorded/);
assert.equal(workNextStep({ state: "held" }).recorded, false);
assert.equal(workNextStep({ state: "held" }).owner, "No agent");
assert.equal(workNextStep(work).title, "On hold", "the state in the board's words");
// No generic line that contradicts the record: a held item's next line never
// says an agent checks it; a picked-up item never says nobody is assigned;
// a suggested item says it needs accepting or cancelling.
assert.doesNotMatch(workNextStep(work).next, /agent needs to|automatic retry/i);
assert.match(workNextStep({ state: "claimed", owner: "fixture-agent" }).next, /^Fixture Agent picked it up/);
assert.doesNotMatch(workNextStep({ state: "claimed", owner: "fixture-agent" }).next, /needs to be assigned|no agent/i);
assert.match(workNextStep({ state: "proposed", owner: "operator:someone" }).next, /accept it or cancel it/);
assert.equal(workNextStep({ state: "verifying", success_predicate: "A real result is checked" }).reason, "The check must show: A real result is checked");
assert.match(workNextStep({ state: "running" }).reason, /No progress note was recorded/);
assert.equal(workNextStep({ state: "ready", next_action: "claim before touching" }).reason, "An agent must pick it up before working on it.", "the tool's step in plain words");
console.log("work-next-step cases ok: latest hold reason, explicit dependencies, resumed work, owner by name, no contradicting generic line");

// One title per work item (lib/work-title.ts). The board card, the trace
// drawer and the item page computed it three ways (outcome first, success
// condition first, "Work details"); now every surface gives the same text.
{
  const { workTitle, workTitleFull, shortenWorkTitle, WORK_TITLE_MAX } = await import("../src/lib/work-title.ts");
  const { buildBoard } = await import("../src/lib/work-pipeline.ts");
  const { mapEnvelopeSpine } = await import("../src/lib/work-trace.ts");
  const { readFileSync } = await import("node:fs");
  const longGoal = `Prepare the fixture release in small parts. ${"Check each listed screen in the fixture notes and write down what changed on it. ".repeat(10)}`;
  const records = [
    { id: "work_title_a", work_id: "work_title_a", state: "running", desired_outcome: "Ship the fixture onboarding screen", success_predicate: "The onboarding screen passes its check on a test device" },
    { id: "work_title_b", work_id: "work_title_b", state: "ready", success_predicate: "The export shows every row  with\nits date" },
    { id: "work_fixture_door_notes_20301009", work_id: "work_fixture_door_notes_20301009", state: "held" },
    { id: "work_0a1b2c3d4e5f", work_id: "work_0a1b2c3d4e5f", state: "verifying", canonical_surface: "product:fixture-app" },
    { id: "work_0f1e2d3c4b5a", work_id: "work_0f1e2d3c4b5a", state: "claimed", canonical_surface: "packet:fixture-release-notes" },
    { id: "work_title_long", work_id: "work_title_long", state: "running", desired_outcome: longGoal },
  ];
  const expected = [
    "Ship the fixture onboarding screen",
    "The export shows every row with its date",
    "Fixture door notes",
    "Fixture app",
    "Fixture release notes",
  ];
  records.slice(0, 5).forEach((record, i) => assert.equal(workTitle(record), expected[i], `title of ${record.id}`));

  const cards = new Map(buildBoard(records, []).flatMap((column) => column.cards).map((card) => [card.workId, card.title]));
  for (const record of records) {
    const drawer = mapEnvelopeSpine(record).title;
    assert.equal(cards.get(record.work_id), workTitle(record), `board card title of ${record.id}`);
    assert.equal(drawer, workTitle(record), `drawer title of ${record.id} matches the card`);
    for (const shown of [workTitle(record), workTitleFull(record)]) {
      assert.doesNotMatch(shown, /^(work details|work item|untitled)$/i, "never a generic placeholder");
    }
  }

  // A goal that is a whole agent prompt: a short form for cards, the full text kept.
  const short = workTitle(records[5]);
  assert.ok(short.length <= WORK_TITLE_MAX, `short form fits: ${short.length}`);
  assert.ok(short.endsWith("…"), "a shortened title says it was shortened");
  assert.ok(workTitleFull(records[5]).startsWith(short.slice(0, -1).trimEnd()), "the short form is the start of the full text");
  assert.equal(workTitleFull(records[5]), longGoal.replace(/\s+/g, " ").trim(), "the full text stays whole");
  assert.equal(shortenWorkTitle("One. Two three four five six seven eight nine ten eleven twelve", 40), "One. Two three four five six seven…", "cuts at a word, not mid-word");
  assert.equal(shortenWorkTitle("First clause is long enough to keep here; then a second clause runs on past the limit", 60), "First clause is long enough to keep here…", "prefers a clause boundary");

  // The item page heading uses the same rule (full text) and no longer has a fallback heading.
  const page = readFileSync(new URL("../src/app/work/[id]/page.tsx", import.meta.url), "utf8");
  assert.match(page, /title=\{workTitleFull\(work\)\}/, "the item page heading is the item's one title");
  assert.doesNotMatch(page, /"Work details"/, "the item page has no generic fallback heading");
  console.log("work-title cases ok: card, drawer and item page share one title; derived titles for records without a goal; long goals shortened, full text kept");
}

// An item's dates and history (lib/work-history.ts), from the JSON
// `lifecycle` list `work show --json` returns: newest first, plain words,
// the agent by name, and the stale mark from the one rule. A fixed clock.
{
  const { workDates, workHistory, plainHistoryNote, daysAgo, plainDay } = await import("../src/lib/work-history.ts");
  const NOW = Date.parse("2030-05-20T12:00:00Z");
  const ns = (days) => String(BigInt(NOW - days * 86_400_000) * 1_000_000n);
  const record = {
    id: "work_fixture_history", state: "running", created_at: new Date(NOW - 30 * 86_400_000).toISOString(),
    lifecycle: [
      { ts: ns(30), from: "", to: "ready", note: "created" },
      { ts: ns(20), from: "ready", to: "claimed", by: "fixture_agent:lane-1", note: "claimed" },
      { ts: ns(19), from: "claimed", to: "running", by: "fixture_agent:lane-1", note: "Started the fixture run. (policy allow_with_obligations)" },
      { ts: ns(2), from: "running", to: "running", by: "fixture_agent:lane-1", note: "action recorded: inspect_task" },
    ],
  };
  const history = workHistory(record);
  assert.deepEqual(history.map((entry) => entry.what), [
    "Updated while running",
    "Picked up, not started → Running",
    "Ready to start → Picked up, not started",
    "Created as Ready to start",
  ], "newest first, in the board's words");
  assert.equal(history[1].who, "Fixture Agent", "who, by the agent's one name");
  assert.equal(history[1].note, "Started the fixture run.", "policy suffix dropped");
  assert.equal(history[0].note, 'Recorded the step "inspect task".');
  assert.equal(history[3].note, "", "a bare 'created' note adds nothing");
  assert.equal(plainHistoryNote("claimed"), "");
  assert.deepEqual(workHistory({ id: "work_fixture_none" }), [], "no lifecycle: no history, never invented");

  const dates = workDates(record, { nowMs: NOW });
  assert.equal(dates.pickedUpMs, NOW - 20 * 86_400_000, "picked up: the latest step into claimed");
  assert.equal(dates.changedMs, NOW - 2 * 86_400_000, "last changed: the latest step");
  assert.equal(dates.createdMs, NOW - 30 * 86_400_000);
  assert.equal(dates.stale, true, "running and picked up 20 days ago");
  assert.equal(workDates(record, { nowMs: NOW, claimedAt: ns(3) }).stale, false, "the workbench pick-up time wins");
  assert.equal(workDates({ ...record, state: "held" }, { nowMs: NOW }).stale, false, "on hold is not in progress");
  assert.equal(workDates({ id: "x", state: "running" }, { nowMs: NOW }).stale, false, "unknown pick-up time is not stale");
  assert.equal(workDates({ id: "x", state: "running" }, { nowMs: NOW }).changedMs, null);
  assert.equal(daysAgo(NOW - 40 * 86_400_000, NOW), "40 days ago");
  assert.equal(daysAgo(NOW - 1000, NOW), "today");
  assert.equal(plainDay(Date.parse("2030-05-01T23:30:00Z")), "1 May 2030");
  console.log("work-history cases ok: lifecycle newest first in plain words, agent by name, dates from the record, one stale rule");
}
