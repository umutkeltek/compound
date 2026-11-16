// Falsifiers for the non-blocking read layer (lib/compound.ts readProjection):
// a page never waits for compound, at most two heavy reads run at once, the
// same read never starts twice, a failure keeps the last good copy, an action
// marks values out of date without deleting them, and two homes never mix.
// Run: bun scripts/read-queue-cases.mjs. The wired gate runs it:
// projection-timeout-smoke.mjs starts it as its own bun process and fails
// when it exits non-zero.
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
await Bun.sleep(Number(process.env.READ_QUEUE_SLEEP_MS ?? "800"));
rmSync(join(state, "running", name), { force: true });
const failFile = join(state, "fail");
if (existsSync(failFile)) { console.error("stub failure"); process.exit(3); }
const payloadFile = join(state, "payload", process.argv.slice(2).join("_") + ".json");
if (existsSync(payloadFile)) { console.log(JSON.stringify({ generated_at: new Date().toISOString(), ...JSON.parse(readFileSync(payloadFile, "utf8")) })); process.exit(0); }
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
  // "loading" is the real falsifier (a waiting read would return the value);
  // the bound stays under the stub's 800 ms run.
  assert.ok(coldMs < 750, `a cold read must not wait for compound (took ${coldMs.toFixed(0)} ms)`);

  // 2. The same read is never started twice; 3. at most two heavy reads at once.
  await api.readProjection(["alpha", "--json"], { timeoutMs: HEAVY });
  await api.runProjection(["alpha", "--json"], { timeoutMs: HEAVY }); // a blocking caller joins it
  for (const name of ["beta", "gamma", "delta", "epsilon"]) await api.readProjection([name, "--json"], { timeoutMs: HEAVY });
  const snapshot = api.readQueueSnapshot();
  assert.ok(snapshot.active.heavy + snapshot.active.light <= 2, `queue over its 2-process limit: ${JSON.stringify(snapshot)}`);
  // Quick reads go first: a light read queued behind heavy ones starts at the next free slot.
  await api.readProjection(["quick", "--json"]);
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

  // 5. Due rule: no sooner than max(25 s, 2 x last run time). The clock is
  // pinned to the stub's own finish stamp, so machine load between steps
  // cannot move the boundary.
  const realNow = Date.now;
  const finishedAt = Date.parse(loaded.generatedAt);
  let offset = 0;
  Date.now = () => finishedAt + offset;
  try {
    offset = 20_000;
    assert.equal((await api.readProjection(["alpha", "--json"], { timeoutMs: HEAVY })).updating, false, "not due before 25 s");
    offset = 28_000;
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

  // The real settings/loops/law page entry points must return while their
  // expensive cold projections are still queued. A blocking read returns
  // values instead and fails both the timing and pending-state assertions.
  const [{ default: Settings }, { default: Loops }, { default: Law }, products, { default: Coordination }] = await Promise.all([
    import("../src/app/settings/page.tsx"), import("../src/app/loops/page.tsx"),
    import("../src/app/standing-law/page.tsx"), import("../src/lib/products.ts"), import("../src/app/traffic/page.tsx"),
  ]);
  const pageStart = performance.now();
  const coldPages = await Promise.all([Settings({}), Loops(), Law(), Coordination({ searchParams: Promise.resolve({}) })]);
  assert.ok(performance.now() - pageStart < 750, "cold settings, loops, law and coordination must not await collectors");
  function nodes(root, name) {
    if (Array.isArray(root)) return root.flatMap((node) => nodes(node, name));
    if (!root || typeof root !== "object") return [];
    return [...(root.type?.name === name ? [root] : []), ...nodes(root.props?.children, name)];
  }
  for (const page of coldPages) assert.ok(nodes(page, "PendingRefresh").some((node) => node.props.pending), "cold pages must arrange a refresh");
  assert.ok(nodes(coldPages[1], "StatTile").every((node) => node.props.value === "—"), "unknown loop counts are not zero");
  const observations = [
    { signalId: "old", provider: "analytics", metric: "downloads", observedAt: "2026-01-01T00:00:00Z" },
    { signalId: "new", provider: "analytics", metric: "downloads", observedAt: "2026-01-03T00:00:00Z" },
    { signalId: "other-provider", provider: "store", metric: "downloads", observedAt: "2026-01-02T00:00:00Z" },
    { signalId: "missing-time", provider: "analytics", metric: "downloads", observedAt: "" },
  ];
  assert.deepEqual(products.latestProductSignals(observations).map((s) => s.signalId), ["new", "other-provider"], "latest metric rows preserve provider identity and ignore older/missing times");
  assert.equal(observations.length, 4, "the view must not mutate historical evidence");
  await api.settleProjectionReads();

  // 10. Two homes never mix: a module instance pinned to another home sees nothing.
  process.env.COMPOUND_HOME = join(scratch, "home-b");
  const other = await import("../src/lib/compound.ts?home-b");
  assert.notEqual(other.COMPOUND_HOME, api.COMPOUND_HOME, "the second instance must pin the second home");
  const otherRead = await other.readProjection(["gamma", "--json"], { timeoutMs: HEAVY });
  assert.equal(otherRead.value, null, "a value read for one home must never be served for another");
  await other.settleProjectionReads();

  // 11. Open work grouped by app or codebase (readWorkProjectIndex): one read
  // over the three cached projections the pages already make. It never waits
  // for compound, a copy without its list is unknown (never an empty index),
  // an unchanged set of copies reuses the built index, and a failed refresh
  // keeps the last good index.
  // Case 8 marked every copy out of date under a clock pinned ahead of real
  // time, so this block runs on its own clock, past that mark.
  const realClock = Date.now;
  const shiftedFrom = realClock();
  Date.now = () => realClock() + 10 * 60_000;
  try {
    assert.ok(Date.now() > shiftedFrom);
    const DAY = 86_400_000;
    const NOW = Date.UTC(2031, 6, 1, 12, 0, 0);
    const payloads = join(state, "payload");
    await mkdir(payloads, { recursive: true });
    const put = (argv, body) => writeFile(join(payloads, argv.join("_") + ".json"), JSON.stringify(body));
    const { work, apps, inventory } = api.WORK_PROJECT_INDEX_READS;
    await put(work.args, { open: [
      { work_id: "work_rq_1", state: "held", canonical_surface: "product:fixture-app" },
      { work_id: "work_rq_2", state: "running", claimed_at: String(BigInt(NOW - 9 * DAY) * 1_000_000n), target_worktree_root: "/home/example/Code/products/fixture-app/feature" },
      { work_id: "work_rq_3", state: "ready", canonical_surface: "-" },
    ] });
    await put(apps.args, { apps: [{ product_id: "product:fixture-app", name: "Fixture App", aliases: ["product:fixture_app"] }] });
    // No inventory payload yet: the stub prints a copy that has no resource list.

    const coldStart = performance.now();
    const cold = await api.readWorkProjectIndex({ nowMs: NOW });
    assert.ok(performance.now() - coldStart < 400, "the index read never waits for compound");
    assert.equal(cold.status, "loading");
    assert.equal(cold.index, null, "no copy yet: the index is unknown, not empty");
    assert.deepEqual([...cold.missing].sort(), ["apps", "inventory", "work"]);
    assert.equal("value" in cold.reads.work, false, "each read comes without its large value");
    await api.settleProjectionReads();

    const partial = await api.readWorkProjectIndex({ nowMs: NOW });
    assert.equal(partial.status, "live");
    assert.equal(partial.index, null, "a copy without its list is unknown, never an empty index");
    assert.deepEqual(partial.missing, ["inventory"]);

    await put(inventory.args, { resources: [{ id: "product:fixture_app", links: [{ rel: "code_in", evidence: "products/fixture-app" }] }] });
    api.markRunJsonCacheOutOfDate();
    const marked = await api.readWorkProjectIndex({ nowMs: NOW });
    assert.equal(marked.status, "stale", "after an action the old copies show as out of date while they refresh");
    await api.settleProjectionReads();
    const built = await api.readWorkProjectIndex({ nowMs: NOW });
    assert.equal(built.status, "live");
    assert.deepEqual(built.missing, []);
    assert.deepEqual(
      built.index.projects.map((p) => [p.id, p.open, p.needYou, p.inProgress, p.stale]),
      [["product:fixture-app", 2, 1, 1, 1], ["none", 1, 0, 0, 0]],
    );
    assert.equal(built.index.byWork.work_rq_2, "product:fixture-app");
    assert.equal((await api.readWorkProjectIndex({ nowMs: NOW })).index, built.index, "unchanged copies reuse the built index");

    await writeFile(join(state, "fail"), "1");
    api.markRunJsonCacheOutOfDate();
    await api.readWorkProjectIndex({ nowMs: NOW });
    await api.settleProjectionReads();
    const failed = await api.readWorkProjectIndex({ nowMs: NOW });
    assert.equal(failed.status, "stale");
    assert.ok(failed.reads.work.error && failed.reads.work.failedAt, "the failure is recorded on its read");
    assert.deepEqual(failed.index?.total, built.index.total, "a failed refresh keeps the last good index");
    await rm(join(state, "fail"), { force: true });
  } finally {
    Date.now = realClock;
  }

  console.log("read-queue-cases ok: non-blocking loading, <=2 compound reads at once, one run per read, due gap, failure keeps last good, out-of-date mark, home-keyed cache, work grouped by project from cached copies");
} finally {
  await rm(scratch, { recursive: true, force: true });
}
