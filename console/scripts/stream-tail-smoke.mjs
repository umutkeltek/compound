/**
 * Executes (not just typechecks) the SSE tail-parser depth test
 * (tail-parser.selftest.ts). Pure TS (no JSX, no Node import), so plain
 * `node` with `--experimental-strip-types` runs it, matching the other
 * console smoke scripts (work-trace-smoke.mjs, move-type-drift-smoke.mjs);
 * `bun scripts/stream-tail-smoke.mjs` works too.
 */
import { runTailParserSelfTest } from "../src/lib/stream/tail-parser.selftest.ts";

const result = runTailParserSelfTest();
for (const line of result.details) {
  console.log(line);
}

if (!result.ok) {
  console.error("stream-tail smoke FAILED");
  process.exit(1);
}

console.log(
  "stream-tail smoke ok: partial-line buffering, truncation reset, and the dedupe key hold under adversarial chunking",
);
