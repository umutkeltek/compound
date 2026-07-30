import assert from "node:assert/strict";
import { chmod, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

/*
 * Honest-degradation acceptance for console projection reads. The executable
 * is always a disposable stub and COMPOUND_HOME is always a disposable home;
 * this smoke never reaches a real Compound instance.
 *
 * Run one falsifier (useful for red -> green evidence):
 *   bun scripts/projection-degradation-smoke.mjs lie
 *   bun scripts/projection-degradation-smoke.mjs never-cache-null
 *   bun scripts/projection-degradation-smoke.mjs no-false-alarms
 *
 * Run the complete acceptance:
 *   bun scripts/projection-degradation-smoke.mjs
 */

const requested = process.argv[2] ?? "all";
const allowed = new Set(["all", "lie", "never-cache-null", "no-false-alarms"]);
assert.ok(allowed.has(requested), `unknown projection-degradation case: ${requested}`);

const scratch = await mkdtemp(join(tmpdir(), "compound-projection-degradation-"));
const bin = join(scratch, "compound-stub");
const home = join(scratch, "home");
const state = join(scratch, "state");

await writeFile(
  bin,
  `#!/usr/bin/env bun
import { existsSync, mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const mode = process.argv[2] ?? "";
const state = process.env.PROJECTION_SMOKE_STATE ?? "";
mkdirSync(state, { recursive: true });

if (mode === "slow") {
  await Bun.sleep(16_000);
  console.log(JSON.stringify({ generated_at: new Date().toISOString(), groups: [] }));
  process.exit(0);
}

if (mode === "cache") {
  const marker = join(state, "cache-succeeded");
  if (!existsSync(marker)) {
    writeFileSync(marker, "ok");
    console.log(JSON.stringify({
      generated_at: new Date().toISOString(),
      marker: "last-good",
      open: [{ work_id: "work_example" }],
    }));
    process.exit(0);
  }
  console.error("simulated projection failure");
  process.exit(7);
}

if (mode === "empty") {
  console.log(JSON.stringify({
    generated_at: new Date().toISOString(),
    counts: { needs_you_groups: 0 },
    groups: [],
  }));
  process.exit(0);
}

console.error("unknown stub mode");
process.exit(2);
`,
  "utf8",
);
await chmod(bin, 0o755);

process.env.COMPOUND_BIN = bin;
process.env.COMPOUND_HOME = home;
process.env.PROJECTION_SMOKE_STATE = state;

try {
  // Pay the one-time OS cold-start tax on this brand-new executable at a
  // brand-new temp path BEFORE any timed read. That tax (code-signing
  // validation + page-cache miss on the first exec) is a property of the host,
  // not of the degradation semantics under test, and on a loaded machine it
  // alone exceeded the seed read's budget — a healthy projection reported as
  // `unreachable` purely from machine load. `empty` is the side-effect-free
  // stub mode, so warming through it seeds nothing the cases depend on.
  await Bun.spawn([bin, "empty"], { stdout: "ignore", stderr: "ignore" }).exited;

  const compound = await import("../src/lib/compound.ts");
  const hasProjectionRead = typeof compound.runProjection === "function";

  let feedRenderModel;
  const feedModelPath = join(import.meta.dir, "../src/components/kit/FeedDegraded.tsx");
  if (await Bun.file(feedModelPath).exists()) {
    ({ feedRenderModel } = await import("../src/components/kit/FeedDegraded.tsx"));
  }

  // The fallback deliberately reconstructs the old value-only contract so the
  // red baseline names the lie instead of failing at module import time.
  const readProjection = async (args, opts) => {
    if (hasProjectionRead) return compound.runProjection(args, opts);
    const value = await compound.runJson(args);
    return {
      status: value === null ? undefined : "live",
      value,
      generatedAt: typeof value?.generated_at === "string" ? value.generated_at : null,
      lastGoodAt: null,
      error: null,
      elapsedMs: 0,
    };
  };
  const renderModel = (read, hasData) =>
    typeof feedRenderModel === "function"
      ? feedRenderModel(read.status, hasData)
      : hasData
        ? "data"
        : "empty";

  async function lie() {
    compound.invalidateRunJsonCache();
    const read = await readProjection(["slow", "--json"], { timeoutMs: 25 });
    const model = renderModel(read, false);
    assert.equal(
      model,
      "degraded",
      `a failed projection derived the ${model} model (the old clean-seam lie)`,
    );
    assert.equal(read.status, "unreachable", "a cold timeout must be explicitly unreachable");
    assert.equal(read.value, null, "an unreachable projection must not invent data");
    assert.ok(read.error, "an unreachable projection must retain its failure reason");
    console.log("  lie ok: cold timeout -> unreachable -> degraded");
  }

  // The "cache"/"empty" stub modes never sleep - they should resolve almost
  // instantly - but a brand-new executable at a brand-new temp path pays a
  // one-time OS cold-start tax on its FIRST exec (measured up to ~300ms on
  // this host; every invocation after that is ~10-20ms). 2s is generous
  // headroom over that tax while staying far below the `lie` case's
  // deliberately-real 16s slow path, so this remains a fast smoke.
  async function neverCacheNull() {
    compound.invalidateRunJsonCache();
    const realNow = Date.now;
    try {
      const first = await readProjection(["cache", "--json"], { timeoutMs: 2_000 });
      assert.equal(first.status, "live", "the successful seed read must be live");
      assert.equal(first.value?.marker, "last-good", "the stub must seed a last-good value");

      Date.now = () => realNow() + 30_000;
      const second = await readProjection(["cache", "--json"], { timeoutMs: 2_000 });
      assert.equal(second.status, "stale", "an expired last-good value must revalidate as stale");

      // The failed background refresh settles on a SUBPROCESS's schedule, not a
      // fixed one: a 100ms sleep raced a `bun` spawn and went red purely from
      // machine load. Converge on the condition instead — re-read (the stub fails
      // every call after the seed, so the refresh failure is durable once it
      // lands) until the failure surfaces, bounded so a genuinely lost error
      // still fails the smoke rather than hanging it.
      let afterFailure = await readProjection(["cache", "--json"], { timeoutMs: 2_000 });
      const settleDeadline = realNow() + 10_000;
      while (!afterFailure.error && realNow() < settleDeadline) {
        await Bun.sleep(20);
        afterFailure = await readProjection(["cache", "--json"], { timeoutMs: 2_000 });
      }
      assert.equal(afterFailure.status, "stale", "a failed refresh with last-good data must stay stale");
      assert.equal(afterFailure.value?.marker, "last-good", "a failed refresh must not cache null over good");
      assert.ok(afterFailure.lastGoodAt, "a failed refresh must preserve lastGoodAt");
      assert.ok(afterFailure.error, "the stale read must expose the latest refresh failure");
    } finally {
      Date.now = realNow;
    }
    console.log("  never-cache-null ok: failed refresh preserved value + lastGoodAt");
  }

  async function noFalseAlarms() {
    compound.invalidateRunJsonCache();
    const read = await readProjection(["empty", "--json"], { timeoutMs: 2_000 });
    const model = renderModel(read, Array.isArray(read.value?.groups) && read.value.groups.length > 0);
    assert.equal(read.status, "live", "a fast valid empty projection must be live");
    assert.equal(model, "empty", "live + genuinely empty must retain the doctrine empty state");
    assert.ok(read.lastGoodAt, "a successful empty read must record lastGoodAt");
    assert.equal(read.error, null, "a successful empty read must not carry a false alarm");
    console.log("  no-false-alarms ok: live + empty -> doctrine empty state");
  }

  console.log(`projection-degradation-smoke (${requested})`);
  if (requested === "all" || requested === "lie") await lie();
  if (requested === "all" || requested === "never-cache-null") await neverCacheNull();
  if (requested === "all" || requested === "no-false-alarms") await noFalseAlarms();
  console.log("projection-degradation-smoke ok");
} finally {
  await rm(scratch, { recursive: true, force: true });
}
