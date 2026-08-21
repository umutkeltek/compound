import { describe, expect, test } from "bun:test";
import { spawnSync } from "node:child_process";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { parseConfig } from "./asc_daily_signals.ts";
import {
  loadFunnelRows,
  parseMetricList,
  planFunnelSeriesApplications,
} from "./asc_funnel_series_bridge.ts";
import { initializeCompoundHome } from "./test_support.ts";

const seriesPath = "fixtures/product/asc-funnel-series.jsonl";

describe("asc_funnel_series_bridge edge", () => {
  test("plans existing Compound funnel-series adapter commands from JSONL rows, mapped by asc_app_id only", () => {
    const config = parseConfig("fixtures/product/asc-products.json");
    const rows = loadFunnelRows(seriesPath);

    const plan = planFunnelSeriesApplications(config.apps, rows, {
      seriesPath,
      beforeWindow: "2026-07-01/2026-07-03",
      afterWindow: "2026-07-08/2026-07-10",
      days: 7,
      changeEvent: "Reworked App Store product page screenshots.",
      metrics: ["impressions"],
      compoundBin: "compound",
    });

    expect(plan.before).toBe("2026-07-01/2026-07-03");
    expect(plan.after).toBe("2026-07-08/2026-07-10");
    // product:other-app (987654321) has no rows at all after 2026-07-03 in the
    // fixture, so it must be skipped with a reason naming exactly that — not
    // silently dropped, and not folded into product:focus-notes's command.
    expect(plan.commands).toHaveLength(1);
    expect(plan.skipped).toEqual([
      {
        product_id: "product:other-app",
        asc_app_id: "987654321",
        reason: "no_rows_in_after_window",
      },
    ]);
    expect(plan.commands[0].args).toEqual([
      "product",
      "signal",
      "--asc-funnel-series",
      seriesPath,
      "--product",
      "product:focus-notes",
      "--app-id",
      "123456789",
      "--before",
      "2026-07-01/2026-07-03",
      "--after",
      "2026-07-08/2026-07-10",
      "--change-event",
      "Reworked App Store product page screenshots.",
      "--metric",
      "impressions",
    ]);
  });

  test("skips an app whose window rows exist but carry no confirmed complete-funnel coverage", () => {
    // product:other-app (987654321) has a row in this exact window
    // (2026-07-02) but its coverage.complete_funnel is false. A bridge that
    // only checked "are there any rows at all" would wrongly plan a command
    // here; the Go adapter would then fail unpredictably instead of the
    // bridge reporting a clear, specific skip reason up front.
    const plan = planFunnelSeriesApplications(
      parseConfig("fixtures/product/asc-products.json").apps,
      loadFunnelRows(seriesPath),
      {
        seriesPath,
        beforeWindow: "2026-07-02/2026-07-02",
        afterWindow: "2026-07-03/2026-07-03",
        days: 7,
        changeEvent: "Reworked App Store product page screenshots.",
        metrics: ["impressions"],
        compoundBin: "compound",
      },
    );

    expect(plan.commands).toHaveLength(0);
    const otherAppSkip = plan.skipped.find((s) => s.product_id === "product:other-app");
    expect(otherAppSkip).toEqual({
      product_id: "product:other-app",
      asc_app_id: "987654321",
      reason: "no_complete_funnel_rows_in_before_window",
    });
  });

  test("derives adjacent before and after windows when explicit ranges are absent", () => {
    const rows = [
      { asc_app_id: "123", date: "2026-07-01", impressions: 1, coverage: { complete_funnel: true } },
      { asc_app_id: "123", date: "2026-07-02", impressions: 2, coverage: { complete_funnel: true } },
      { asc_app_id: "123", date: "2026-07-03", impressions: 3, coverage: { complete_funnel: true } },
      { asc_app_id: "123", date: "2026-07-04", impressions: 4, coverage: { complete_funnel: true } },
      { asc_app_id: "123", date: "2026-07-05", impressions: 5, coverage: { complete_funnel: true } },
      { asc_app_id: "123", date: "2026-07-06", impressions: 6, coverage: { complete_funnel: true } },
    ];

    const plan = planFunnelSeriesApplications(
      [{ product_id: "product:demo", asc_app_id: "123" }],
      rows,
      {
        seriesPath,
        afterEnd: "2026-07-06",
        days: 2,
        changeEvent: "Synthetic change.",
        metrics: ["impressions", "product_page_views"],
        compoundBin: "compound",
      },
    );

    expect(plan.before).toBe("2026-07-03/2026-07-04");
    expect(plan.after).toBe("2026-07-05/2026-07-06");
    expect(plan.commands.map((command) => command.metric)).toEqual([
      "impressions",
      "product_page_views",
    ]);
    expect(plan.skipped).toEqual([]);
  });

  test("rejects unsupported funnel metrics before invoking Compound", () => {
    expect(() => parseMetricList("impressions,not_a_metric")).toThrow(
      "unsupported funnel metric not_a_metric",
    );
  });

  test("apply invokes Compound funnel-series adapter idempotently in temp home", () => {
    const home = mkdtempSync(join(tmpdir(), "compound-asc-funnel-bridge-home-"));
    const bin = join(mkdtempSync(join(tmpdir(), "compound-asc-funnel-bridge-bin-")), "compound");
    const build = spawnSync("go", ["build", "-o", bin, "./cmd/compound"], {
      encoding: "utf8",
    });
    if (build.status !== 0) {
      throw new Error(build.stderr || build.stdout);
    }

    const env = initializeCompoundHome(bin, home);
    const args = [
      "tools/asc_funnel_series_bridge.ts",
      "--config",
      "fixtures/product/asc-products.json",
      "--funnel-series",
      seriesPath,
      "--change-event",
      "Reworked App Store product page screenshots.",
      "--before",
      "2026-07-01/2026-07-03",
      "--after",
      "2026-07-08/2026-07-10",
      "--metric",
      "impressions",
      "--compound-bin",
      bin,
      "--apply",
      "--json",
    ];
    for (let i = 0; i < 2; i++) {
      const run = spawnSync(process.execPath, args, { encoding: "utf8", env });
      if (run.status !== 0) {
        throw new Error(run.stderr || run.stdout);
      }
      const report = JSON.parse(run.stdout);
      expect(report.status).toBe("ok");
      expect(report.planned).toBe(1);
      expect(report.applied).toBe(1);
      expect(report.apply_results).toEqual([
        {
          product_id: "product:focus-notes",
          asc_app_id: "123456789",
          metric: "impressions",
          signal_count: 1,
          transition_count: 1,
        },
      ]);
      // The 999999-impressions, complete_funnel=false decoy rows the fixture
      // plants in both windows must never surface here.
      expect(report.skipped).toEqual([
        {
          product_id: "product:other-app",
          asc_app_id: "987654321",
          reason: "no_rows_in_after_window",
        },
      ]);
    }

    const timeline = spawnSync(bin, ["product", "timeline", "product:focus-notes"], {
      encoding: "utf8",
      env,
    });
    if (timeline.status !== 0) {
      throw new Error(timeline.stderr || timeline.stdout);
    }
    expect(timeline.stdout).toContain("app_store_connect_impressions 300 -> 400");
  }, 15000);
});
