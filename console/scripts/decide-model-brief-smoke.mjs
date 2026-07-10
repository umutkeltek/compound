/**
 * Executes (not just typechecks) `decide-model-brief.selftest.ts` — the
 * decision-brief console falsifiers (scan-every-member, invalid-brief
 * rejection, frozen unbriefed card, copy-only argv). Runs under `bun`:
 * decide-model.ts resolves `@/lib/...` path aliases, which plain
 * `node --experimental-strip-types` cannot (same reason as
 * generic-actions-smoke.mjs).
 */
import { runDecideModelBriefSelfTest } from "../src/lib/decide-model-brief.selftest.ts";

const result = runDecideModelBriefSelfTest();
for (const line of result.details) {
  console.log(line);
}

if (!result.ok) {
  console.error("decide-model-brief smoke FAILED");
  process.exit(1);
}

console.log(
  "decide-model-brief smoke ok: briefs surface from any group member, invalid briefs never render, unbriefed cards stay frozen",
);
