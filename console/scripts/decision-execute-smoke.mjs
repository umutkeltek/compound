/**
 * Executes (not just typechecks) `decision-execute.selftest.ts` — the
 * execute-route ↔ decide-model agreement falsifiers (a spec'd area with zero
 * executable options for an item must honor a projection actionId via the
 * governed generic pathway; a spec that can act always wins). Runs under
 * `bun`: decision-execute/decision-registry resolve `@/lib` path aliases,
 * which plain `node --experimental-strip-types` cannot.
 */
import { runDecisionExecuteSelfTest } from "../src/lib/decision-execute.selftest.ts";

const result = runDecisionExecuteSelfTest();
for (const line of result.details) {
  console.log(line);
}

if (!result.ok) {
  console.error("decision-execute smoke FAILED");
  process.exit(1);
}

console.log(
  "decision-execute smoke ok: generic fallback fires exactly when the bespoke spec cannot act and a projection action is named",
);
