import { describe, expect, test } from "bun:test";
import {
  decideGuardAction,
  parseDeletedOpenFiles,
  parseLoadedModels,
  parseElapsedSeconds,
  type DeletedOpenFile,
  type ModelProcess,
} from "./disk_pressure_guard";

const GIB = 1024 ** 3;
const NOW_MS = 1_800_000_000_000;
const giant: DeletedOpenFile = {
  pid: 92740,
  command: "node",
  fd: "17u",
  sizeBytes: 24_223_629_225,
  path: "/private/tmp/deleted-model",
};
const idleModel: ModelProcess = {
  identifier: "model-local",
  status: "idle",
  ttlSeconds: null,
  lastUsedAtMs: NOW_MS - 7200 * 1000,
};

describe("deleted-open file parser", () => {
  test("retains process identity across multiple file records", () => {
    const rows = parseDeletedOpenFiles([
      "p92740",
      "cnode",
      "f17u",
      "s24223629225",
      "n/private/tmp/deleted-model",
      "f18u",
      "s1024",
      "n/private/tmp/small",
      "p42",
      "cother",
      "f3u",
      "s9000",
      "n/private/tmp/other",
    ].join("\n"));
    expect(rows).toEqual([
      giant,
      {
        pid: 92740,
        command: "node",
        fd: "18u",
        sizeBytes: 1024,
        path: "/private/tmp/small",
      },
      {
        pid: 42,
        command: "other",
        fd: "3u",
        sizeBytes: 9000,
        path: "/private/tmp/other",
      },
    ]);
  });

  test("parses LM Studio TTL and last-use evidence", () => {
    expect(parseLoadedModels(JSON.stringify([{
      identifier: "ssd-guard-test",
      status: "IDLE",
      ttlMs: 60_000,
      lastUsedTime: NOW_MS,
    }]))).toEqual([{
      identifier: "ssd-guard-test",
      status: "idle",
      ttlSeconds: 60,
      lastUsedAtMs: NOW_MS,
    }]);
  });
});

describe("guard decision", () => {
  const base = {
    freeBytes: 90 * GIB,
    freeFloorBytes: 140 * GIB,
    giantFiles: [giant],
    lmStudioHolderPids: new Set([giant.pid]),
    holderAgesSeconds: new Map([[giant.pid, 7200]]),
    models: [idleModel],
    hasActiveClients: false,
    minimumIdleSeconds: 1800,
    nowMs: NOW_MS,
  };

  test("unloads only a proven old idle LM Studio holder", () => {
    expect(decideGuardAction(base).action).toBe("unload_idle_lmstudio");
  });

  test("does nothing when free space is healthy", () => {
    expect(decideGuardAction({ ...base, freeBytes: 150 * GIB }).action).toBe("none");
  });

  test("refuses when a local client is connected", () => {
    expect(decideGuardAction({ ...base, hasActiveClients: true }).action).toBe("report_only");
  });

  test("refuses a generic giant deleted-file holder", () => {
    expect(decideGuardAction({ ...base, lmStudioHolderPids: new Set() }).action).toBe("report_only");
  });

  test("refuses an active or recent model", () => {
    expect(decideGuardAction({
      ...base,
      models: [{ ...idleModel, status: "generating" }],
    }).action).toBe("report_only");
    expect(decideGuardAction({
      ...base,
      holderAgesSeconds: new Map([[giant.pid, 60]]),
    }).action).toBe("report_only");
    expect(decideGuardAction({
      ...base,
      models: [{ ...idleModel, lastUsedAtMs: NOW_MS - 60 * 1000 }],
    }).action).toBe("report_only");
  });
});

describe("elapsed process age", () => {
  test("parses macOS ps elapsed formats", () => {
    expect(parseElapsedSeconds("17:08")).toBe(1028);
    expect(parseElapsedSeconds("02:17:08")).toBe(8228);
    expect(parseElapsedSeconds("2-03:17:08")).toBe(184628);
    expect(parseElapsedSeconds("not-a-duration")).toBeNull();
  });
});
