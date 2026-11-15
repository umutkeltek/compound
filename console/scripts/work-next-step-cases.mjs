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
