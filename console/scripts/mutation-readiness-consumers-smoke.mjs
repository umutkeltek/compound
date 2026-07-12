import assert from "node:assert/strict";
import fs from "node:fs";

const standardActionPages = [
  "src/app/page.tsx",
  "src/app/reviews/page.tsx",
  "src/app/memory/page.tsx",
  "src/app/routing/page.tsx",
  "src/app/sources/page.tsx",
];

for (const file of standardActionPages) {
  const source = fs.readFileSync(file, "utf8");
  assert.ok(
    source.includes("const mutationsEnabled = computeConsoleMutationReadiness(consoleMutationsEnabled()).ready;"),
    `${file} must expose only the exact effective .ready conjunction`,
  );
  assert.ok(
    source.includes("mutationsEnabled={mutationsEnabled}") || source.includes("mutationsReady={mutationsEnabled}"),
    `${file} must pass effective readiness into its live-action child`,
  );
}

const briefing = fs.readFileSync("src/app/page.tsx", "utf8");
assert.ok(
  briefing.includes("<ContinueCard model={continueModel} mutationsReady={mutationsEnabled} />"),
  "briefing must pass effective readiness into Continue",
);

const work = fs.readFileSync("src/app/work/page.tsx", "utf8");
assert.ok(
  work.includes("const mutationsReady = computeConsoleMutationReadiness(consoleMutationsEnabled()).ready;"),
  "work board must consume the exact effective .ready conjunction",
);
assert.ok(work.includes("mutationsReady={mutationsReady}"), "work board must pass effective readiness into drawers");
const detail = fs.readFileSync("src/app/work/[id]/page.tsx", "utf8");
assert.ok(
  detail.includes("const mutationsReady = computeConsoleMutationReadiness(consoleMutationsEnabled()).ready;"),
  "work detail must consume the exact effective .ready conjunction",
);
assert.ok(detail.includes("mutationsReady={mutationsReady}"), "work detail must pass effective readiness into actions");

const settings = fs.readFileSync("src/app/settings/page.tsx", "utf8");
assert.ok(
  settings.includes("const mutationReadiness = computeConsoleMutationReadiness(consoleMutationsEnabled());") &&
    settings.includes("mutationsEnabled={mutationReadiness.ready}") &&
    settings.includes("mutationFlagEnabled={mutationReadiness.flagEnabled}") &&
    settings.includes("instancePinned={mutationReadiness.instancePinned}"),
  "settings must distinguish effective readiness from its two inputs",
);

console.log("mutation-readiness consumer smoke ok: every action page consumes flag + frozen pin");
