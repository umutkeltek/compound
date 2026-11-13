import assert from "node:assert/strict";
import { installProjectionDiskProbe, runProjectionDiskProbe } from "./projection-cache-invalidation.selftest.mjs";
import { chmod, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

const scratch = await mkdtemp(join(tmpdir(), "compound-projection-timeout-"));
const bin = join(scratch, "compound-stub");

await writeFile(
  bin,
  `#!/usr/bin/env bun
if (process.argv[2] !== "timeout-escalation") {
  console.error("unexpected stub command");
  process.exit(2);
}
await Bun.sleep(120);
console.log(JSON.stringify({
  generated_at: new Date().toISOString(),
  marker: "extended-caller-succeeded",
  probe: process.argv[3] ?? "",
}));
`,
  "utf8",
);
await chmod(bin, 0o755);

process.env.COMPOUND_BIN = bin;
process.env.COMPOUND_HOME = join(scratch, "home");
// invalidateRunJsonCache() deletes the disk tier: point it at a fixture-owned
// folder so the smoke never deletes the operator console's real cache.
process.env.COMPOUND_CONSOLE_CACHE_DIR = join(scratch, "console-cache");

try {
  const diskProbe = installProjectionDiskProbe();
  const api = await import("../src/lib/compound.ts");
  await runProjectionDiskProbe(api, diskProbe);
  const { runProjectionTimeoutSelfTest } = await import("../src/lib/projection-timeout.selftest.ts");
  const result = await runProjectionTimeoutSelfTest();
  assert.equal(result.ok, true);
  // The read-queue cases run as their own bun process, not an import: they
  // pin COMPOUND_HOME and COMPOUND_BIN at import, the read cache lives on
  // globalThis, they move Date.now, and this smoke replaces node:fs/promises.
  // One process would let either side's state leak into the other. The cases
  // make their own scratch folder and cache folder (COMPOUND_CONSOLE_CACHE_DIR).
  const cases = Bun.spawn([process.execPath, join(import.meta.dir, "read-queue-cases.mjs")], {
    stdout: "inherit",
    stderr: "inherit",
    env: process.env,
  });
  assert.equal(await cases.exited, 0, "read-queue cases failed (output above)");
  console.log(`projection-timeout-smoke ok: ${result.details}; read-queue cases ok`);
} finally {
  await rm(scratch, { recursive: true, force: true });
}
