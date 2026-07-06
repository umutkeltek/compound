import assert from "node:assert/strict";

import { selectHighestLeverageGate } from "./briefing";

const older = "2026-07-01T10:00:00Z";
const newer = "2026-07-05T10:00:00Z";

const selected = selectHighestLeverageGate([
  {
    id: "old-low",
    title: "Old low leverage",
    consequence: "clears one item",
    downstreamWorkCount: 1,
    createdAt: older,
  },
  {
    id: "new-high",
    title: "New high leverage",
    consequence: "clears three items",
    downstreamWorkCount: 3,
    createdAt: newer,
  },
]);

assert.equal(selected?.id, "new-high");

const tie = selectHighestLeverageGate([
  {
    id: "newer",
    title: "Newer tie",
    consequence: "clears two items",
    downstreamWorkCount: 2,
    createdAt: newer,
  },
  {
    id: "older",
    title: "Older tie",
    consequence: "clears two items",
    downstreamWorkCount: 2,
    createdAt: older,
  },
]);

assert.equal(tie?.id, "older");

console.log("briefing selector test passed");
