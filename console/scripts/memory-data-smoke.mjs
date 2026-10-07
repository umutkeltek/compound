/**
 * Executes (not just typechecks) `memory-data.selftest.ts` — the keep-aside-
 * drain falsifier (work_4033b98c5837): an operator-adjudicated parked standing-
 * law candidate must leave the /memory needs-your-call queue, while an
 * untouched (never-adjudicated) parked decision candidate must still render.
 * Runs under `bun`: memory-data.ts resolves the `@/lib` path alias, which
 * plain `node --experimental-strip-types` cannot.
 */
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { runMemoryDataSelfTest } from "../src/lib/memory-data.selftest.ts";
import "./evidence-card-smoke.mjs";

// A fresh process lets the fixture pin its binary/home before Compound's
// module-level runtime identity is resolved by any other imported smoke.
const readHonesty = spawnSync(process.execPath, [fileURLToPath(new URL("./memory-read-honesty-smoke.mjs", import.meta.url))], {
  stdio: "inherit", timeout: 90_000,
});
if (readHonesty.error || readHonesty.status !== 0) {
  console.error("memory-read honesty smoke FAILED", readHonesty.error ?? readHonesty.status);
  process.exit(1);
}

const result = runMemoryDataSelfTest();
for (const line of result.details) {
  console.log(line);
}

if (!result.ok) {
  console.error("memory-data smoke FAILED");
  process.exit(1);
}

console.log(
  "memory-data smoke ok: operator-adjudicated parks leave the standing-law queue; untouched parks still render",
);
