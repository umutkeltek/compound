/**
 * THE flag-unset falsifier for the governed stream door (packet: stream-door).
 *
 * Drives GET /api/stream directly (importing the route handler — no Next server
 * needed) with COMPOUND_CONSOLE_PRIVATE_FALLBACKS UNSET, against a throwaway
 * COMPOUND_HOME, and proves:
 *   1. the stream is LIVE in the default launch: `event: ready` reports
 *      {"streaming":true} (the pre-verb route reported streaming:false here —
 *      that is the red baseline this smoke turns green);
 *   2. a real transition is delivered when an event is appended to the test
 *      home's work.jsonl;
 *   3. the per-client `compound stream --follow` child is KILLED on client
 *      disconnect (no orphaned tail process survives an abort).
 *
 * Imports extensionless TS via the @/ alias, so it runs under bun (not the node
 * strip-types runner). Requires a compound binary WITH the `stream` verb: set
 * COMPOUND_BIN to a fresh build, or this smoke builds one to a temp path.
 */
import assert from "node:assert/strict";
import { execFileSync, spawn, spawnSync } from "node:child_process";
import { existsSync, mkdirSync, appendFileSync, writeFileSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(HERE, "..", "..");

function log(msg) {
  console.log(`stream-door-smoke: ${msg}`);
}

// ---- resolve a compound binary that has the `stream` verb ----
function resolveBinary() {
  const fromEnv = process.env.COMPOUND_BIN;
  if (fromEnv && existsSync(fromEnv)) return fromEnv;
  const out = path.join(mkdtempSync(path.join(tmpdir(), "stream-door-bin-")), "compound");
  log(`building compound -> ${out}`);
  execFileSync("go", ["build", "-o", out, "./cmd/compound"], { cwd: REPO_ROOT, stdio: "inherit" });
  return out;
}

// ---- inspect live `compound stream --follow` PIDs for a binary ----
// The operator may run legitimate followers — even with THIS exact binary
// (COMPOUND_BIN override mode). Process proofs therefore never assert on
// counts: they capture the PID set before the route opens and assert only on
// the delta this run spawned. Baseline followers must survive untouched.
function followPids(binaryPath) {
  const escapedBinary = binaryPath.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const res = spawnSync("pgrep", ["-f", `${escapedBinary} stream --follow --cursor`], { encoding: "utf8" });
  if (res.error) return null; // inspection unavailable
  if (res.status === 1) return new Set(); // pgrep: no matches
  if (res.status !== 0) return null;
  return new Set(res.stdout.split("\n").map((l) => l.trim()).filter(Boolean));
}

// ---- read an SSE ReadableStream until predicate(text) or timeout ----
async function readUntil(reader, predicate, timeoutMs) {
  const decoder = new TextDecoder();
  let text = "";
  const deadline = Date.now() + timeoutMs;
  if (predicate(text)) return text;
  while (Date.now() < deadline) {
    const remaining = deadline - Date.now();
    let timer;
    const timeout = new Promise((res) => {
      timer = setTimeout(() => res({ __timeout: true }), remaining);
    });
    const result = await Promise.race([reader.read(), timeout]);
    clearTimeout(timer);
    if (result.__timeout) break;
    if (result.value) text += decoder.decode(result.value, { stream: true });
    if (predicate(text)) return text;
    if (result.done) break;
  }
  return text;
}

const binary = resolveBinary();

const home = mkdtempSync(path.join(tmpdir(), "stream-door-home-"));
mkdirSync(path.join(home, "events"), { recursive: true });
mkdirSync(path.join(home, "ledger"), { recursive: true });
const workLog = path.join(home, "events", "work.jsonl");
writeFileSync(workLog, `{"ts":"t1","work_id":"work_seed","from":"","to":"open"}\n`);
writeFileSync(path.join(home, "ledger", "seq"), "1");

// Freeze the module-pinned env BEFORE importing the route (compound.ts resolves
// COMPOUND_HOME/COMPOUND_BIN once at module load).
process.env.COMPOUND_HOME = home;
process.env.COMPOUND_BIN = binary;
delete process.env.COMPOUND_CONSOLE_PRIVATE_FALLBACKS; // THE falsifier condition

// FALSIFIER (override mode): a legitimate follower already runs with the SAME
// binary this smoke uses. A count-based or binary-scoped-count proof counts it
// as an orphan and fails; the PID-delta proof must leave it alive.
let decoy = null;
let baseline = followPids(binary);
if (baseline !== null) {
  decoy = spawn(binary, ["stream", "--follow", "--cursor", "1"], {
    env: { ...process.env, COMPOUND_HOME: home },
    stdio: "ignore",
  });
  for (let i = 0; i < 20; i++) {
    baseline = followPids(binary);
    if (baseline !== null && baseline.has(String(decoy.pid))) break;
    await new Promise((r) => setTimeout(r, 100));
  }
  assert.ok(
    baseline !== null && baseline.has(String(decoy.pid)),
    "baseline follower never became visible to process inspection",
  );
  log(`baseline follower live (pid ${decoy.pid}) before the route opens`);
}

const { GET } = await import("../src/app/api/stream/route.ts");

const NEW_WORK_ID = "work_streamed42";
const controller = new AbortController();
let failure = null;

try {
  const request = new Request("https://compound.invalid/api/stream", { signal: controller.signal });
  const response = await GET(request);
  assert.equal(response.headers.get("Content-Type"), "text/event-stream", "must be an SSE response");
  const reader = response.body.getReader();

  // (1) ready streaming:true — the heart of the slice.
  const ready = await readUntil(reader, (t) => t.includes("event: ready"), 5_000);
  assert.ok(ready.includes("event: ready"), "no ready frame arrived");
  assert.ok(
    ready.includes('"streaming":true'),
    `flag-unset stream is not live — ready frame was:\n${ready.split("\n\n")[1] ?? ready}`,
  );
  log("ready frame reports streaming:true (flag unset)");

  // (2) a real transition is delivered on append.
  appendFileSync(workLog, `{"ts":"t2","work_id":"${NEW_WORK_ID}","from":"","to":"claimed"}\n`);
  const transition = await readUntil(
    reader,
    (t) => t.includes("event: transition") && t.includes(NEW_WORK_ID),
    8_000,
  );
  assert.ok(
    transition.includes("event: transition") && transition.includes(NEW_WORK_ID),
    `appended event never streamed through as a transition; buffer so far:\n${transition}`,
  );
  log(`transition frame delivered the appended event ${NEW_WORK_ID}`);

  // (3) the follow children this run spawned die on disconnect, while the
  // legitimate baseline follower (same binary — override mode) survives.
  const during = followPids(binary);
  if (baseline === null || during === null) {
    if (baseline !== null) {
      // Inspection worked at baseline capture: a later failure is a broken
      // proof, not a skip — fail closed instead of reporting a false green.
      assert.ok(during !== null, "process inspection failed after initial success");
    }
    log("pgrep unavailable — skipping child-exit assertion");
  } else {
    const delta = [...during].filter((pid) => !baseline.has(pid));
    assert.ok(delta.length >= 1, "expected this run to spawn a `compound stream --follow` child while streaming");
    controller.abort();
    // give the route's abort handler + SIGTERM time to land
    let survivors = delta;
    for (let i = 0; i < 40 && survivors.length > 0; i++) {
      await new Promise((r) => setTimeout(r, 100));
      const now = followPids(binary);
      assert.ok(now !== null, "process inspection failed after initial success");
      survivors = delta.filter((pid) => now.has(pid));
    }
    assert.equal(survivors.length, 0, "follow child survived client disconnect (orphaned tail process)");
    if (decoy) {
      assert.equal(decoy.exitCode, null, "legitimate baseline follower was killed by the smoke's disconnect proof");
      const finalPids = followPids(binary);
      assert.ok(finalPids !== null, "process inspection failed after initial success");
      assert.ok(finalPids.has(String(decoy.pid)), "legitimate baseline follower vanished from process inspection");
    }
    log("run-spawned follow child exited on client disconnect; baseline follower survived");
  }
} catch (error) {
  failure = error;
} finally {
  controller.abort(); // ensure no orphan even if an assertion threw
  await new Promise((r) => setTimeout(r, 200));
  if (decoy) decoy.kill("SIGTERM"); // the smoke owns its decoy; never leaves it behind
}

if (failure) {
  console.error(`stream-door-smoke FAILED: ${failure.message}`);
  process.exit(1);
}

console.log(
  "stream-door smoke ok: default launch (flag unset) streams live via `compound stream`, delivers appended transitions, and kills the follow child on disconnect",
);
