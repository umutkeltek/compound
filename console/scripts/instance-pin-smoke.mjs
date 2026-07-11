import assert from "node:assert/strict";
import { homedir } from "node:os";
import { resolve } from "node:path";

/**
 * Falsifier for the 2026-07-11 cross-port isolation breach (finding
 * rvf_7f1321314811; misattributed decisions cld_5eabdf8f72d6 / cld_9b63382def8d):
 * a console clone started against a scratch home on another port routed a
 * MUTATING act to the canonical ~/.compound. Root cause: the console resolved
 * COMPOUND_HOME fail-OPEN — an unset env silently bound the canonical home, and
 * nothing refused the mutation. This proves the pin: an instance is bound to ONE
 * explicit home at start, an unpinned instance CANNOT mutate (fails closed), and
 * a scratch-A pin never resolves to scratch-B or the canonical default.
 *
 * Runs under `node --experimental-strip-types`, so instance-pin.ts must import
 * only node: builtins (no local extensionless imports it cannot resolve).
 */

const { computeInstancePin, instanceUnpinnedBlockedResponse, INSTANCE_PIN } =
  await import("../src/lib/instance-pin.ts");
const { COMPOUND_HOME } = await import("../src/lib/compound.ts");

const canonical = resolve(`${homedir()}/.compound`);
const scratchA = "/scratch/A/.compound";
const scratchB = "/scratch/B/.compound";

// 1. Unset COMPOUND_HOME is the breach condition: it resolves to canonical, and
//    the pin flags it as NOT explicit so the mutation gate can fail closed.
const unpinned = computeInstancePin({});
assert.equal(unpinned.explicit, false, "unset COMPOUND_HOME must be flagged not-explicit");
assert.equal(unpinned.home, canonical, "unset COMPOUND_HOME resolves canonical (the fail-open default)");

// 2. An unpinned instance must REFUSE to mutate (fail closed) — this is the
//    guarantee that closes the breach: no explicit home, no write.
const blockedUnpinned = instanceUnpinnedBlockedResponse(unpinned);
assert.ok(blockedUnpinned, "unpinned instance must block mutations");
assert.equal(blockedUnpinned.status, 409, "unpinned mutation must fail closed with 409");

// 3. An explicitly pinned instance is allowed, and is pinned to its OWN home —
//    never scratch-B, never the canonical default.
const pinnedA = computeInstancePin({ COMPOUND_HOME: scratchA });
assert.equal(pinnedA.explicit, true, "explicit COMPOUND_HOME must be flagged explicit");
assert.equal(pinnedA.home, resolve(scratchA), "pin binds its own home");
assert.notEqual(pinnedA.home, resolve(scratchB), "scratch-A pin must never equal scratch-B");
assert.notEqual(pinnedA.home, canonical, "scratch-A pin must never equal the canonical default");
assert.equal(instanceUnpinnedBlockedResponse(pinnedA), null, "explicitly pinned instance may mutate");

// 4. Home identity: distinct homes hash distinct; two spellings of the same
//    absolute home hash identical (relative spellings can't smuggle a mismatch).
const pinnedB = computeInstancePin({ COMPOUND_HOME: scratchB });
assert.notEqual(pinnedA.homeHash, pinnedB.homeHash, "distinct homes must hash distinct");
assert.ok(/^[0-9a-f]+$/.test(pinnedA.homeHash), "home hash is short hex");
assert.equal(pinnedA.homeBasename, ".compound", "basename surfaces the home leaf");
const pinnedARel = computeInstancePin({ COMPOUND_HOME: "/scratch/A/../A/.compound" });
assert.equal(pinnedARel.homeHash, pinnedA.homeHash, "same absolute home => same hash regardless of spelling");

// 5. The exec home runCompound injects IS the frozen pin — one bound home, not a
//    per-request value. (Both read process.env at load; they must never drift.)
assert.equal(INSTANCE_PIN.home, COMPOUND_HOME, "runCompound exec home must equal the frozen instance pin");

console.log(
  "instance-pin smoke ok: mutations pinned to the started home; unpinned instance fails closed; scratch-A never resolves scratch-B/canonical",
);
