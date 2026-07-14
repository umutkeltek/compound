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
import { execFileSync, spawnSync } from "node:child_process";
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

// ---- count live `compound stream --follow` children (pgrep; null if absent) ----
function countFollowChildren() {
  const res = spawnSync("pgrep", ["-f", "stream --follow --cursor"], { encoding: "utf8" });
  if (res.error) return null; // pgrep unavailable on this host: skip the child-exit assertion
  if (res.status === 1) return 0; // pgrep: no matches
  if (res.status !== 0) return null;
  return res.stdout.split("\n").filter((l) => l.trim()).length;
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

const { GET } = await import("../src/app/api/stream/route.ts");

const NEW_WORK_ID = "work_streamed42";
const controller = new AbortController();
let failure = null;

try {
  const request = new Request("http://localhost:3000/api/stream", { signal: controller.signal });
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

  // (3) the follow child is alive now, and dies on disconnect.
  const before = countFollowChildren();
  if (before === null) {
    log("pgrep unavailable — skipping child-exit assertion");
  } else {
    assert.ok(before >= 1, "expected a live `compound stream --follow` child while streaming");
    controller.abort();
    // give the route's abort handler + SIGTERM time to land
    let after = before;
    for (let i = 0; i < 40 && after > 0; i++) {
      await new Promise((r) => setTimeout(r, 100));
      after = countFollowChildren() ?? 0;
    }
    assert.equal(after, 0, "follow child survived client disconnect (orphaned tail process)");
    log("follow child exited on client disconnect");
  }
} catch (error) {
  failure = error;
} finally {
  controller.abort(); // ensure no orphan even if an assertion threw
  await new Promise((r) => setTimeout(r, 200));
}

if (failure) {
  console.error(`stream-door-smoke FAILED: ${failure.message}`);
  process.exit(1);
}

console.log(
  "stream-door smoke ok: default launch (flag unset) streams live via `compound stream`, delivers appended transitions, and kills the follow child on disconnect",
);
