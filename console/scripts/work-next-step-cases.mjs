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
assert.equal(workNextStep(work).owner, "fixture-agent");
assert.equal(workNextStep({ ...work, blocking_reason: "Current dependency" }).reason, "Current dependency");
assert.doesNotMatch(workNextStep({ ...work, state: "ready" }).reason, /test account|test device/);
assert.match(workNextStep({ state: "held" }).reason, /no reason was recorded/);
assert.equal(workNextStep({ state: "held" }).owner, "No agent assigned");
assert.match(workNextStep(work).next, /No automatic retry is confirmed/);
assert.equal(workNextStep({ state: "verifying", success_predicate: "A real result is checked" }).reason, "A real result is checked");
assert.match(workNextStep({ state: "running" }).reason, /marked as running/);
console.log("work-next-step cases ok: latest hold reason, explicit dependencies, resumed work, missing ownership and unconfirmed retries");

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
