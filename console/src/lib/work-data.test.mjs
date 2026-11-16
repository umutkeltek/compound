import { afterAll, expect, mock, test } from "bun:test";
import { isValidElement } from "react";
import * as compound from "./compound";

let read = {
  status: "loading", value: null, generatedAt: null, lastGoodAt: null,
  error: null, failedAt: null, updating: true, outOfDate: false, warn: false,
  ageMs: null, lastRunMs: null, source: "compound workbench --json", elapsedMs: 0,
};
const readProjection = mock(async () => read);
mock.module("./compound", () => ({
  ...compound,
  readProjection,
  getWorkbench: () => { throw new Error("detail must not await broad workbench collector"); },
  getWork: async () => ({ id: "work_cold_detail", state: "verifying", desired_outcome: "A real task", success_predicate: "Native proof" }),
  getWorkLeases: async () => null,
}));
afterAll(() => mock.restore());
const { readWorkbenchItem } = await import("./work-data");
const { default: WorkDetail } = await import("../app/work/[id]/page");
const pageProps = (page) => JSON.stringify(page, (_key, value) => isValidElement(value) ? value.props : value);

test("cold detail renders native work while supplemental collection is pending", async () => {
  const lookup = await readWorkbenchItem("work_cold_detail");
  expect(lookup.item).toBeNull();
  expect(lookup.read.status).toBe("loading");
  const page = await WorkDetail({ params: Promise.resolve({ id: "work_cold_detail" }) });
  const output = pageProps(page);
  expect(output).toContain("A real task");
  expect(output).toContain("Native proof");
  expect(output).toContain('"pending":true');
  expect(readProjection).toHaveBeenCalledWith(["workbench", "--json"], { timeoutMs: compound.HEAVY_PROJECTION_TIMEOUT_MS });
});

test("later detail reads receive enrichment and retain stale failure evidence", async () => {
  read = { ...read, status: "stale", value: { recent: [{ work_id: "work_cold_detail", receipt_id: "rcpt_last_good" }] }, lastGoodAt: "2026-01-01T00:00:00Z", error: "collector failed", warn: true };
  const lookup = await readWorkbenchItem("work_cold_detail");
  expect(lookup.item?.receipt_id).toBe("rcpt_last_good");
  expect(lookup.read.error).toBe("collector failed");
  const page = await WorkDetail({ params: Promise.resolve({ id: "work_cold_detail" }) });
  expect(pageProps(page)).toContain("rcpt_last_good");
  read = { ...read, status: "unreachable", value: null, lastGoodAt: null, updating: false };
  const failed = await readWorkbenchItem("work_cold_detail");
  expect(failed.item).toBeNull();
  expect(failed.read.status).toBe("unreachable");
  expect(failed.read.error).toBe("collector failed");
});
