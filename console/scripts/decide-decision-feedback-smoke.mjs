/**
 * Executes (not just typechecks) `decide-decision-feedback.selftest.ts` —
 * falsifiers for the Decide-page empty-required-field silent no-op and the
 * bare "Owner action unavailable" label (work_6047d1a871ed slice 1). Runs
 * under `bun`, not plain `node --experimental-strip-types`: the selftest
 * resolves `@/lib`-style-adjacent relative imports into governed-action-
 * client.ts, which itself carries an `import type` from decision-registry.ts
 * (erased at build, so it stays client-bundle-safe) — same reason
 * generic-actions-smoke.mjs and decide-model-brief-smoke.mjs run under bun.
 * Not yet wired into `compound proof-check`'s gate list (a Go change,
 * cmd/compound/proof_check.go, out of scope here) — same as those two.
 */
import { runDecideDecisionFeedbackSelfTest } from "../src/lib/decide-decision-feedback.selftest.ts";

const result = runDecideDecisionFeedbackSelfTest();
for (const line of result.details) {
  console.log(line);
}

if (!result.ok) {
  console.error("decide-decision-feedback smoke FAILED");
  process.exit(1);
}

console.log(
  "decide-decision-feedback smoke ok: empty-required-field feedback and named-owner unavailable copy are wired at their render sites",
);
