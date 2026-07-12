import assert from "node:assert/strict";
import fs from "node:fs";

const actionPages = [
  "src/app/page.tsx",
  "src/app/reviews/page.tsx",
  "src/app/memory/page.tsx",
  "src/app/routing/page.tsx",
  "src/app/settings/page.tsx",
  "src/app/sources/page.tsx",
  "src/app/work/page.tsx",
  "src/app/work/[id]/page.tsx",
];

for (const file of actionPages) {
  const source = fs.readFileSync(file, "utf8");
  assert.ok(
    source.includes("computeConsoleMutationReadiness") && source.includes("consoleMutationsEnabled()"),
    `${file} must compose the mutation flag with the frozen instance pin`,
  );
  assert.ok(
    !source.includes("const mutationsEnabled = consoleMutationsEnabled();"),
    `${file} must not expose the raw mutation flag as effective readiness`,
  );
}

const briefing = fs.readFileSync("src/app/page.tsx", "utf8");
assert.ok(
  briefing.includes("<ContinueCard model={continueModel} mutationsReady={mutationsEnabled} />"),
  "briefing must pass effective readiness into Continue",
);

const work = fs.readFileSync("src/app/work/page.tsx", "utf8");
assert.ok(work.includes("mutationsReady={mutationsReady}"), "work board must pass effective readiness into drawers");
const detail = fs.readFileSync("src/app/work/[id]/page.tsx", "utf8");
assert.ok(detail.includes("mutationsReady={mutationsReady}"), "work detail must pass effective readiness into actions");

console.log("mutation-readiness consumer smoke ok: every action page consumes flag + frozen pin");
