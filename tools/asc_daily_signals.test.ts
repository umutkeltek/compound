import { describe, expect, test } from "bun:test";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { exportsForSnapshot, parseConfig, signalExport } from "./asc_daily_signals.ts";

describe("asc_daily_signals edge", () => {
  test("maps every configured app metric to deterministic ProductSignalExport documents", () => {
    const config = parseConfig("fixtures/product/asc-products.json");
    const snapshot = JSON.parse(readFileSync("fixtures/product/asc-daily-signals.json", "utf8"));
    const exports = exportsForSnapshot(config, snapshot);

    expect(exports).toHaveLength(9);
    expect(exports[0].schema_version).toBe("compound.product_signal_export.v0.1");
    expect(exports[0].source.kind).toBe("app_store_connect");
    expect(exports[0].metric.before).toEqual(exports[0].metric.after);
    expect(exports.map((doc) => doc.product_id)).toContain("product:focus-notes");
    expect(exports.map((doc) => doc.metric.name)).toContain("product_page_conversion_rate");

    const again = exportsForSnapshot(config, snapshot);
    expect(again.map((doc) => doc.signal_id)).toEqual(exports.map((doc) => doc.signal_id));
  });

  test("aliases downloads and proceeds to existing ASC metric names", () => {
    const downloads = signalExport(
      { product_id: "product:demo", asc_app_id: "123" },
      "downloads",
      7,
      "2026-07-13",
      "2026-07-13T12:00:00Z",
    );
    const proceeds = signalExport(
      { product_id: "product:demo", asc_app_id: "123" },
      "proceeds",
      3.5,
      "2026-07-13",
      "2026-07-13T12:00:00Z",
    );
    expect(downloads.metric.name).toBe("units");
    expect(proceeds.metric.name).toBe("developer_proceeds_usd");
  });

  test("accepts operator-owned YAML app config", () => {
    const dir = mkdtempSync(join(tmpdir(), "compound-asc-config-"));
    const path = join(dir, "asc-products.yaml");
    writeFileSync(
      path,
      `apps:
  - product_id: product:focus-notes
    asc_app_id: "123456789"
    surface: app-store-page
    metrics:
      - units
      - impressions
`,
    );
    const config = parseConfig(path);
    expect(config.apps).toHaveLength(1);
    expect(config.apps[0].metrics).toEqual(["units", "impressions"]);
  });
});
