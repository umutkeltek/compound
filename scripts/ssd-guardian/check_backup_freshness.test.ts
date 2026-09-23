import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { STATUSES, backupGateAllowsApply, type Status } from "./check_backup_freshness";

describe("backupGateAllowsApply", () => {
  test("only a fresh receipt clears a destructive apply", () => {
    expect(backupGateAllowsApply("fresh")).toBe(true);
    expect(backupGateAllowsApply("stale")).toBe(false);
    expect(backupGateAllowsApply("missing")).toBe(false);
  });

  test("every status the checker can emit has a defined verdict", () => {
    // If someone adds a Status, this forces them to decide its verdict here
    // rather than letting the gate fall through to "refuse".
    const verdicts = new Map<Status, boolean>(STATUSES.map((s) => [s, backupGateAllowsApply(s)]));
    expect([...verdicts.keys()].sort()).toEqual(["fresh", "missing", "stale"]);
    expect([...verdicts.values()].filter(Boolean)).toHaveLength(1);
  });

  test("refuses a status the checker never emits", () => {
    // The original bug: the gate compared against "ok", so a fresh backup
    // read as not-ok and every apply run aborted while logging green.
    expect(backupGateAllowsApply("ok")).toBe(false);
    expect(backupGateAllowsApply("")).toBe(false);
  });
});

describe("weekly_reclaim gate wiring", () => {
  const source = readFileSync(join(import.meta.dir, "weekly_reclaim.ts"), "utf8");

  test("calls the shared gate instead of re-spelling a status literal", () => {
    expect(source).toContain("backupGateAllowsApply(j.status)");
  });

  test("does not compare j.status against a bare string literal", () => {
    // Any `j.status === "..."` / `!== "..."` is the drift that caused the bug.
    expect(source).not.toMatch(/j\.status\s*[!=]==\s*["']/);
  });
});
