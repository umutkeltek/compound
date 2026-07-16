import assert from "node:assert/strict";

import { invalidateRunJsonCache, runProjection } from "./compound";

export async function runProjectionTimeoutSelfTest(): Promise<{ ok: true; details: string }> {
  invalidateRunJsonCache();

  const argv = ["timeout-escalation", "--json"];
  const shortReadPromise = runProjection(argv, { timeoutMs: 25 });
  await new Promise((resolve) => setTimeout(resolve, 5));
  const extendedReadPromise = runProjection(argv, { timeoutMs: 2_000 });

  const [shortRead, extendedRead] = await Promise.all([shortReadPromise, extendedReadPromise]);
  assert.equal(shortRead.status, "unreachable", "the intentionally short caller must time out");
  assert.equal(
    extendedRead.status,
    "live",
    "a longer projection caller must not inherit an already-running shorter timeout",
  );
  assert.equal(extendedRead.value?.marker, "extended-caller-succeeded");

  return {
    ok: true,
    details: "longer caller escaped shorter in-flight timeout and published live projection data",
  };
}
