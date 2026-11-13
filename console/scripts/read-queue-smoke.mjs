// Falsifiers for the non-blocking read layer (lib/compound.ts readProjection):
// a page never waits for compound, at most two heavy reads run at once, the
// same read never starts twice, a failure keeps the last good copy, an action
// marks values out of date without deleting them, and two homes never mix.
// Run: bun scripts/read-queue-smoke.mjs
import assert from "node:assert/strict";
import { chmod, mkdir, mkdtemp, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

const scratch = await mkdtemp(join(tmpdir(), "compound-read-queue-"));
const bin = join(scratch, "compound-stub");
const state = join(scratch, "state");
const cacheDir = join(scratch, "cache");
await mkdir(join(state, "running"), { recursive: true });
await mkdir(join(state, "runs"), { recursive: true });

// The stub records every run and how many runs overlap, sleeps, then prints
// JSON (or fails when the mode file says so).
await writeFile(
  bin,
  `#!/usr/bin/env bun
import { existsSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
const state = process.env.READ_QUEUE_STATE;
const name = process.argv.slice(2).join("_") + "." + process.pid;
writeFileSync(join(state, "runs", name), "");
writeFileSync(join(state, "running", name), "");
const overlap = readdirSync(join(state, "running")).length;
const peakFile = join(state, "peak");
const peak = existsSync(peakFile) ? Number(readFileSync(peakFile, "utf8")) : 0;
if (overlap > peak) writeFileSync(peakFile, String(overlap));
await Bun.sleep(Number(process.env.READ_QUEUE_SLEEP_MS ?? "150"));
rmSync(join(state, "running", name), { force: true });
const failFile = join(state, "fail");
if (existsSync(failFile)) { console.error("stub failure"); process.exit(3); }
console.log(JSON.stringify({ generated_at: new Date().toISOString(), argv: process.argv.slice(2), marker: "ok" }));
`,
  "utf8",
);
await chmod(bin, 0o755);

process.env.COMPOUND_BIN = bin;
process.env.COMPOUND_HOME = join(scratch, "home-a");
process.env.READ_QUEUE_STATE = state;
process.env.COMPOUND_CONSOLE_CACHE_DIR = cacheDir;
// Pay the first-exec cost of a new executable before timing anything.
await Bun.spawn([bin, "warm"], { stdout: "ignore", stderr: "ignore", env: process.env }).exited;
await rm(join(state, "runs"), { recursive: true, force: true });
await mkdir(join(state, "runs"), { recursive: true });
await writeFile(join(state, "peak"), "0");

const runsOf = async (prefix) => (await readdir(join(state, "runs"))).filter((n) => n.startsWith(prefix)).length;
const HEAVY = 180_000;

try {
  const api = await import("../src/lib/compound.ts");

  // 1. A missing value returns at once as "loading" and its read starts.
  const started = performance.now();
  const cold = await api.readProjection(["alpha", "--json"], { timeoutMs: HEAVY });
  const coldMs = performance.now() - started;
  assert.equal(cold.status, "loading", "a missing value must return as loading");
  assert.equal(cold.value, null);
  assert.equal(cold.updating, true, "the first read must already be queued or running");
  assert.ok(coldMs < 100, `a cold read must not wait for compound (took ${coldMs.toFixed(0)} ms)`);

  // 2. The same read is never started twice; 3. at most two heavy reads at once.
  await api.readProjection(["alpha", "--json"], { timeoutMs: HEAVY });
  await api.runProjection(["alpha", "--json"], { timeoutMs: HEAVY }); // a blocking caller joins it
  for (const name of ["beta", "gamma", "delta", "epsilon"]) await api.readProjection([name, "--json"], { timeoutMs: HEAVY });
  const snapshot = api.readQueueSnapshot();
  assert.ok(snapshot.active.heavy <= 2, `heavy lane over its limit: ${JSON.stringify(snapshot)}`);
  await api.settleProjectionReads();
  assert.equal(await runsOf("alpha"), 1, "the same read must run once, not once per caller");
  const peak = Number(await Bun.file(join(state, "peak")).text());
  assert.ok(peak <= 2, `at most two compound reads may overlap (saw ${peak})`);
  assert.ok(peak >= 2, `the queue must use both heavy slots (saw ${peak})`);

  // 4. A loaded value returns with its age; a fresh value is not re-read.
  const loaded = await api.readProjection(["alpha", "--json"], { timeoutMs: HEAVY });
  assert.equal(loaded.status, "live");
  assert.equal(loaded.value?.marker, "ok");
  assert.equal(loaded.updating, false, "a value inside its refresh gap must not be re-read");
  assert.ok(typeof loaded.ageMs === "number" && loaded.ageMs < 60_000);
  assert.ok(typeof loaded.lastRunMs === "number" && loaded.lastRunMs >= 100, "the run time is measured");

  // 5. Due rule: no sooner than max(25 s, 2 x last run time).
  const realNow = Date.now;
  let offset = 0;
  Date.now = () => realNow() + offset;
  try {
    offset = 20_000;
    assert.equal((await api.readProjection(["alpha", "--json"], { timeoutMs: HEAVY })).updating, false, "not due before 25 s");
    offset = 26_000;
    const due = await api.readProjection(["alpha", "--json"], { timeoutMs: HEAVY });
    assert.equal(due.updating, true, "due after 25 s");
    assert.equal(due.value?.marker, "ok", "the old copy is served while it refreshes");
    await api.settleProjectionReads();
    assert.equal(await runsOf("alpha"), 2);

    // 6. A failed refresh keeps the last good copy and records the failure.
    await writeFile(join(state, "fail"), "1");
    offset += 30_000;
    await api.readProjection(["alpha", "--json"], { timeoutMs: HEAVY });
    await api.settleProjectionReads();
    const failed = await api.readProjection(["alpha", "--json"], { timeoutMs: HEAVY });
    assert.equal(failed.value?.marker, "ok", "a failed refresh must keep the last good value");
    assert.equal(failed.status, "stale");
    assert.ok(failed.error && failed.failedAt, "the failure is recorded with when and why");
    assert.equal(failed.warn, true, "a failed refresh earns the warning colour");

    // 7. A failed first read is a failure, never an empty value or "loading".
    await api.readProjection(["never", "--json"], { timeoutMs: HEAVY });
    await api.settleProjectionReads();
    const never = await api.readProjection(["never", "--json"], { timeoutMs: HEAVY });
    assert.equal(never.status, "unreachable");
    assert.equal(never.value, null);
    assert.ok(never.error);
    await rm(join(state, "fail"), { force: true });

    // 8. An action marks values out of date; it does not delete them.
    offset += 60_000;
    await api.readProjection(["beta", "--json"], { timeoutMs: HEAVY });
    await api.settleProjectionReads();
    const before = await api.readProjection(["beta", "--json"], { timeoutMs: HEAVY });
    assert.equal(before.status, "live");
    offset += 1_000;
    api.markRunJsonCacheOutOfDate();
    const marked = await api.readProjection(["beta", "--json"], { timeoutMs: HEAVY });
    assert.equal(marked.value?.marker, "ok", "an action must keep the last good copy");
    assert.equal(marked.outOfDate, true);
    assert.notEqual(marked.status, "live", "an out-of-date copy is never live");
    assert.equal(marked.updating, true, "an action makes the value due at once");
    const blocking = await api.runProjection(["beta", "--json"], { timeoutMs: HEAVY });
    assert.equal(blocking.status, "stale", "the blocking reader also treats it as out of date, without waiting");
    await api.settleProjectionReads();
    const refreshed = await api.readProjection(["beta", "--json"], { timeoutMs: HEAVY });
    assert.equal(refreshed.outOfDate, false, "a read that started after the action clears the mark");
    const markFiles = (await readdir(cacheDir)).filter((n) => !n.endsWith(".json"));
    assert.ok(markFiles.some((n) => n.includes("out-of-date")), "the mark is persisted beside the disk tier");
  } finally {
    Date.now = realNow;
  }

  // 9. The cache folder is the configured one.
  assert.ok((await readdir(cacheDir)).some((n) => n.endsWith(".json")), "values are written to COMPOUND_CONSOLE_CACHE_DIR");

  // 10. Two homes never mix: a module instance pinned to another home sees nothing.
  process.env.COMPOUND_HOME = join(scratch, "home-b");
  const other = await import("../src/lib/compound.ts?home-b");
  assert.notEqual(other.COMPOUND_HOME, api.COMPOUND_HOME, "the second instance must pin the second home");
  const otherRead = await other.readProjection(["gamma", "--json"], { timeoutMs: HEAVY });
  assert.equal(otherRead.value, null, "a value read for one home must never be served for another");
  await other.settleProjectionReads();

  console.log("read-queue-smoke ok: non-blocking loading, <=2 heavy reads, one run per read, due gap, failure keeps last good, out-of-date mark, home-keyed cache");
} finally {
  await rm(scratch, { recursive: true, force: true });
}
