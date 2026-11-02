import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { createTestTempScope } from "./test_support.ts";
import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import {
  CANONICAL_HEADERS,
  ascEnv,
  campaignIdentities,
  campaignsPayload,
  needsCampaignList,
  parseArgs,
  parseConfig,
  reportPayload,
  reportRows,
  rowsForReport,
  writeCsvs,
} from "./apple_ads_daily.ts";

const temporary = createTestTempScope();
afterAll(() => temporary.cleanup());

const REPORT = JSON.parse(readFileSync("fixtures/product/apple-ads-campaign-report.json", "utf8"));
const V1_REPORT = JSON.parse(readFileSync("fixtures/product/apple-ads-platform-v1-report.json", "utf8"));
const V1_CAMPAIGNS = JSON.parse(readFileSync("fixtures/product/apple-ads-platform-v1-campaigns.json", "utf8"));

function buildCompound(): string {
  const bin = join(temporary.dir("compound-apple-ads-bin-"), "compound");
  const build = spawnSync("go", ["build", "-o", bin, "./cmd/compound"], { encoding: "utf8", timeout: 120_000 });
  if (build.error) throw build.error;
  if (build.status !== 0) throw new Error(build.stderr || build.stdout);
  return bin;
}

// Compile in setup, outside the per-test timeout; teardown also runs on build failure.
let COMPOUND_BIN: string;
beforeAll(() => {
  COMPOUND_BIN = buildCompound();
}, 120_000);

describe("apple_ads_daily edge", () => {
  test("maps campaign rows to products through asc_app_id and reports unmapped apps", () => {
    const config = parseConfig("fixtures/product/asc-products.json");
    const rows = rowsForReport(config, REPORT);

    expect(rows.campaigns).toBe(3);
    expect(rows.unmapped).toEqual([{ adamId: "999999999", campaignName: "Unmapped App Try" }]);
    expect(Object.keys(rows.byProduct)).toEqual(["product:focus-notes"]);
    const recs = rows.byProduct["product:focus-notes"];
    expect(recs).toHaveLength(6);
    expect(rows.dateStart).toBe("2026-09-01");
    expect(rows.dateEnd).toBe("2026-09-03");
    const first = recs[0];
    expect(first).toEqual({
      date: "2026-09-01",
      campaign_id: "900000001",
      campaign_name: "Focus Notes US — Search Results",
      spend: "12.50",
      impressions: 1200,
      clicks: 40,
      conversions: 9,
      currency: "USD",
    });
    // Same input → same rows (the import door dedupes on identity, not order).
    expect(rowsForReport(config, REPORT)).toEqual(rows);
  });

  test("the platform v1 report plus its campaign list gives the same rows as the removed v5 report", () => {
    // `asc ads v5` was removed in asc 6.0.0. The v1 report carries the campaign id
    // but neither the campaign name nor its app; the campaign list carries both.
    // The CSV the ledger imports must not change because the API route changed.
    const config = parseConfig("fixtures/product/asc-products.json");
    expect(needsCampaignList(V1_REPORT)).toBe(true);
    expect(needsCampaignList(REPORT)).toBe(false);
    expect(rowsForReport(config, V1_REPORT, V1_CAMPAIGNS)).toEqual(rowsForReport(config, REPORT));
  });

  test("a v1 report without the campaign list is refused instead of writing an empty CSV", () => {
    const config = parseConfig("fixtures/product/asc-products.json");
    const rows = rowsForReport(config, V1_REPORT);
    expect(rows.byProduct).toEqual({});
    expect(rows.unmapped).toHaveLength(3);
    expect(rows.unmapped[0].adamId).toBe("");
  });

  test("the campaign list supplies each campaign's name and app", () => {
    const identities = campaignIdentities(V1_CAMPAIGNS);
    expect(identities.get("900000001")).toEqual({ name: "Focus Notes US — Search Results", adamId: "123456789" });
    expect(identities.get("900000003")).toEqual({ name: "Unmapped App Try", adamId: "999999999" });
    // A campaign that promotes something that is not an app has no app id at all.
    expect(campaignIdentities({ result: [{ id: 7, name: "Web", promotedObjectType: "WEBPAGE", promotedObjectId: 3 }] }).get("7")).toEqual({
      name: "Web",
      adamId: "",
    });
  });

  test("both report shapes keep one row per campaign and day", () => {
    expect(reportRows(REPORT)).toHaveLength(3);
    expect(reportRows(V1_REPORT)).toHaveLength(3);
    expect(reportRows(V1_REPORT)[0].campaignId).toBe("900000001");
    expect(reportRows(V1_REPORT)[0].days).toHaveLength(3);
    expect(reportRows(REPORT)[0].days).toHaveLength(3);
  });

  test("writes one canonical CSV per product with the headers the ads importer understands", () => {
    const rows = rowsForReport({ apps: [{ product_id: "product:focus-notes", asc_app_id: "123456789" }] }, REPORT);
    const outDir = temporary.dir("apple-ads-daily-");
    const csvs = writeCsvs(rows, outDir);
    expect(Object.keys(csvs)).toEqual(["product:focus-notes"]);
    const text = readFileSync(csvs["product:focus-notes"], "utf8");
    const lines = text.trim().split("\n");
    expect(lines[0]).toBe(CANONICAL_HEADERS.join(","));
    expect(lines).toHaveLength(7);
    expect(lines[1]).toBe("2026-09-01,900000001,Focus Notes US — Search Results,12.50,1200,40,9,USD");
  });

  test("the v1 report writes the same canonical CSV as the v5 report", () => {
    const config = { apps: [{ product_id: "product:focus-notes", asc_app_id: "123456789" }] };
    const v5Csv = readFileSync(writeCsvs(rowsForReport(config, REPORT), temporary.dir("apple-ads-v1-csv-"))["product:focus-notes"], "utf8");
    const v1Csv = readFileSync(
      writeCsvs(rowsForReport(config, V1_REPORT, V1_CAMPAIGNS), temporary.dir("apple-ads-v1-csv-"))["product:focus-notes"],
      "utf8",
    );
    expect(v1Csv).toBe(v5Csv);
  });

  test("self-built report request covers the last N days ending today on the v1 time range", () => {
    const payload = reportPayload(14, new Date("2026-09-15T13:00:00Z"));
    const range = payload.timeRange as { start: string; end: string; timeZone: string; granularity: string };
    expect(range.start).toBe("2026-09-02");
    expect(range.end).toBe("2026-09-15");
    expect(range.timeZone).toBe("UTC");
    expect(range.granularity).toBe("DAILY");
    expect(payload.fields).toContain("localSpend");
    expect(payload.pagination).toEqual({ offset: 0, pageSize: 1000 });
    expect(() => reportPayload(0)).toThrow("--days");
    expect(campaignsPayload().pagination).toEqual({ offset: 0, pageSize: 200 });
  });

  test("the asc environment drops proxies and cannot reach for the keychain", () => {
    const env = ascEnv({ HTTPS_PROXY: "http://proxy.invalid:1", https_proxy: "x", ALL_PROXY: "y", PATH: "/usr/bin" });
    expect(env.PATH).toBe("/usr/bin");
    expect(env.HTTPS_PROXY).toBeUndefined();
    expect(env.https_proxy).toBeUndefined();
    expect(env.ALL_PROXY).toBeUndefined();
    expect(env.ASC_ADS_BYPASS_KEYCHAIN).toBe("1");
    expect(env.ASC_ADS_STRICT_AUTH).toBe("1");
  });

  test("argument parsing refuses ambiguous or missing sources", () => {
    expect(() => parseArgs(["--config", "c.json"])).toThrow();
    expect(() => parseArgs(["--config", "c.json", "--report-json", "r.json", "--asc-command", "asc"])).toThrow();
    expect(() => parseArgs(["--config", "c.json", "--org", "1"])).toThrow();
    expect(parseArgs(["--config", "c.json", "--org", "21590290", "--days", "14"]).days).toBe(14);
    const args = parseArgs(["--config", "c.json", "--report-json", "r.json", "--account", "21590290", "--apply", "--json"]);
    expect(args.apply).toBe(true);
    expect(args.json).toBe(true);
    expect(args.account).toBe("21590290");
    expect(args.compoundBin).toBe("compound");
    expect(parseArgs(["--config", "c.json", "--report-json", "r.json", "--campaigns-json", "c2.json"]).campaignsJson).toBe("c2.json");
  });

  test("apply invokes the ads import door idempotently in a temp home", () => {
    const home = temporary.dir("compound-apple-ads-home-");
    const bin = COMPOUND_BIN;
    const env = { ...process.env, COMPOUND_HOME: home };
    const init = spawnSync(bin, ["home", "init", "--home", home], { encoding: "utf8", env });
    if (init.status !== 0) throw new Error(init.stderr || init.stdout);

    const args = [
      "tools/apple_ads_daily.ts",
      "--config",
      "fixtures/product/asc-products.json",
      "--report-json",
      "fixtures/product/apple-ads-campaign-report.json",
      "--account",
      "21590290",
      "--out-dir",
      join(home, "state", "apple-ads-daily"),
      "--compound-bin",
      bin,
      "--apply",
      "--json",
    ];
    const expectedRows = [
      { rows_new: 6, rows_unchanged: 0 },
      { rows_new: 0, rows_unchanged: 6 },
    ];
    for (let i = 0; i < 2; i++) {
      const run = spawnSync(process.execPath, args, { encoding: "utf8", env });
      if (run.status !== 0) throw new Error(run.stderr || run.stdout);
      const report = JSON.parse(run.stdout);
      expect(report.status).toBe("partial"); // one unmapped adamId is reported, never silently dropped
      expect(report.apply_results).toHaveLength(1);
      expect(report.apply_results[0].product).toBe("product:focus-notes");
      expect(report.apply_results[0].status).toBe(0);
      expect(report.apply_results[0].import.rows_new).toBe(expectedRows[i].rows_new);
      expect(report.apply_results[0].import.rows_unchanged).toBe(expectedRows[i].rows_unchanged);
      expect(report.apply_results[0].import.account).toBe("21590290");
    }

    const projection = spawnSync(bin, ["product", "ads", "--json", "--as-of", "2026-09-03", "--window", "3"], { encoding: "utf8", env });
    if (projection.status !== 0) throw new Error(projection.stderr || projection.stdout);
    const ads = JSON.parse(projection.stdout);
    expect(ads.status).toBe("ok");
    expect(ads.totals.spend).toBeCloseTo(39.15, 2);
    expect(ads.totals.conversions).toBe(29);
    expect(ads.platforms.find((p: { platform: string }) => p.platform === "apple_search_ads").status).toBe("connected");
  });

  test("apply imports the same rows from the platform v1 report", () => {
    const home = temporary.dir("compound-apple-ads-v1-home-");
    const bin = COMPOUND_BIN;
    const env = { ...process.env, COMPOUND_HOME: home };
    const init = spawnSync(bin, ["home", "init", "--home", home], { encoding: "utf8", env });
    if (init.status !== 0) throw new Error(init.stderr || init.stdout);

    const args = [
      "tools/apple_ads_daily.ts",
      "--config",
      "fixtures/product/asc-products.json",
      "--report-json",
      "fixtures/product/apple-ads-platform-v1-report.json",
      "--campaigns-json",
      "fixtures/product/apple-ads-platform-v1-campaigns.json",
      "--account",
      "21590290",
      "--out-dir",
      join(home, "state", "apple-ads-daily"),
      "--compound-bin",
      bin,
      "--apply",
      "--json",
    ];
    const run = spawnSync(process.execPath, args, { encoding: "utf8", env });
    if (run.status !== 0) throw new Error(run.stderr || run.stdout);
    const report = JSON.parse(run.stdout);
    expect(report.campaigns).toBe(3);
    expect(report.apply_results[0].status).toBe(0);
    expect(report.apply_results[0].import.rows_new).toBe(6);

    const projection = spawnSync(bin, ["product", "ads", "--json", "--as-of", "2026-09-03", "--window", "3"], { encoding: "utf8", env });
    if (projection.status !== 0) throw new Error(projection.stderr || projection.stdout);
    const ads = JSON.parse(projection.stdout);
    expect(ads.totals.spend).toBeCloseTo(39.15, 2);
    expect(ads.totals.conversions).toBe(29);
  });

  test("a v1 report without its campaign list fails loudly", () => {
    const run = spawnSync(
      process.execPath,
      [
        "tools/apple_ads_daily.ts",
        "--config",
        "fixtures/product/asc-products.json",
        "--report-json",
        "fixtures/product/apple-ads-platform-v1-report.json",
        "--out-dir",
        temporary.dir("apple-ads-v1-nolist-"),
        "--json",
      ],
      { encoding: "utf8" },
    );
    expect(run.status).not.toBe(0);
    expect(run.stderr).toContain("campaign list");
  });
});
