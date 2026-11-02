#!/usr/bin/env bun
import { spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

/*
 * Apple Search Ads → ads ledger bridge.
 *
 * Compound never calls Apple Ads itself. This tool takes a campaign report the
 * `asc` CLI already produced (or runs the commands you hand it), maps every
 * campaign's app (adamId) to a Compound product through the SAME private
 * config the ASC daily signals use (`asc_app_id`), writes one canonical CSV
 * per product, and — only with --apply — invokes the existing
 * `compound product ads import` door. No fetcher, no second store.
 *
 * Report shapes understood:
 *  - Apple Ads Platform API v1 `POST v1/reports/apps/campaigns/query`
 *    (`asc ads reports apps campaigns --file report.json --ad-account <id>`):
 *    result.rows[].{metadata{id},granularMetrics[]{date,impressions,taps,
 *    totalInstalls,localSpend{amount,currency}}}
 *  - Campaign Management API v5 `POST v5/reports/campaigns`
 *    (`asc ads v5 reports campaigns --file payload.json --org <org>`), removed
 *    in asc 6.0.0:
 *    data.reportingDataResponse.row[].{metadata{campaignId,campaignName,app.adamId},
 *    granularity[]{date,impressions,taps,totalInstalls,localSpend{amount,currency}}}
 *
 * The v1 campaign report carries the campaign id but NEITHER the campaign name
 * NOR its app, so the campaign list (`asc ads campaigns find`) supplies both. A
 * v1 report without that list cannot be mapped to a product, and the tool
 * refuses instead of writing an empty CSV.
 */

type AppConfig = { product_id: string; asc_app_id?: string };
type Config = { apps: AppConfig[] };

export type CanonicalAdRow = {
  date: string;
  campaign_id: string;
  campaign_name: string;
  spend: string;
  impressions: number;
  clicks: number;
  conversions: number;
  currency: string;
};

export type BridgeRows = {
  byProduct: Record<string, CanonicalAdRow[]>;
  unmapped: { adamId: string; campaignName: string }[];
  campaigns: number;
  dateStart: string;
  dateEnd: string;
};

/** What the campaign list says about one campaign: its name and the app it promotes. */
export type CampaignIdentity = { name: string; adamId: string };

export const CANONICAL_HEADERS = ["date", "campaign_id", "campaign_name", "spend", "impressions", "clicks", "conversions", "currency"] as const;

export function parseConfig(path: string): Config {
  const raw = JSON.parse(readFileSync(path, "utf8")) as Partial<Config>;
  if (!raw || !Array.isArray(raw.apps)) throw new Error(`config ${path}: expected { apps: [...] }`);
  return { apps: raw.apps.filter((a): a is AppConfig => !!a && typeof a.product_id === "string") };
}

function num(v: unknown): number {
  const n = typeof v === "number" ? v : Number(String(v ?? "").replace(/[^0-9.-]/g, ""));
  return Number.isFinite(n) ? n : 0;
}

function pickRows(...candidates: unknown[]): Record<string, unknown>[] {
  for (const c of candidates) if (Array.isArray(c)) return c as Record<string, unknown>[];
  return [];
}

/** The v1 campaign list: campaign id → campaign name and the app it promotes. */
export function campaignIdentities(campaigns: unknown): Map<string, CampaignIdentity> {
  const out = new Map<string, CampaignIdentity>();
  const payload = (campaigns ?? {}) as Record<string, unknown>;
  const result = (payload.result ?? payload) as Record<string, unknown>;
  const rows = pickRows(result.rows, result.row, Array.isArray(result) ? result : undefined);
  for (const row of rows) {
    const r = row as Record<string, unknown>;
    const id = String(r.id ?? r.campaignId ?? "");
    if (!id) continue;
    const type = r.promotedObjectType ? String(r.promotedObjectType) : "APPSTORE_APP";
    out.set(id, {
      name: String(r.name ?? r.campaignName ?? id),
      adamId: type === "APPSTORE_APP" ? String(r.promotedObjectId ?? r.adamId ?? "") : "",
    });
  }
  return out;
}

export type ReportRow = {
  campaignId: string;
  adamId: string;
  campaignName: string;
  days: Record<string, unknown>[];
};

/** Every report row, in the v5 shape or the v1 shape. */
export function reportRows(report: unknown): ReportRow[] {
  const r = (report ?? {}) as Record<string, unknown>;
  const data = (r.data ?? {}) as Record<string, unknown>;
  const reporting = (data.reportingDataResponse ?? {}) as Record<string, unknown>;
  const result = (r.result ?? {}) as Record<string, unknown>;
  const rows = pickRows(reporting.row, reporting.rows, data.row, data.rows, r.row, r.rows, result.rows, result.row);
  return rows.map((row) => {
    const md = (row.metadata ?? {}) as Record<string, unknown>;
    const app = (md.app ?? {}) as Record<string, unknown>;
    const days = Array.isArray(row.granularity) ? row.granularity : Array.isArray(row.granularMetrics) ? row.granularMetrics : [];
    return {
      campaignId: String(md.campaignId ?? md.id ?? ""),
      adamId: String(app.adamId ?? md.adamId ?? md.appId ?? ""),
      campaignName: String(md.campaignName ?? ""),
      days: days as Record<string, unknown>[],
    };
  });
}

/** True when the report carries campaign ids but no app ids: the v1 shape. */
export function needsCampaignList(report: unknown): boolean {
  const rows = reportRows(report);
  return rows.length > 0 && rows.every((row) => !row.adamId);
}

/** Pure: report JSON + product config → canonical rows per product. */
export function rowsForReport(config: Config, report: unknown, campaigns?: unknown): BridgeRows {
  const app2product = new Map<string, string>();
  for (const app of config.apps) if (app.asc_app_id) app2product.set(String(app.asc_app_id), app.product_id);
  const identities = campaigns === undefined ? new Map<string, CampaignIdentity>() : campaignIdentities(campaigns);

  const out: BridgeRows = { byProduct: {}, unmapped: [], campaigns: 0, dateStart: "", dateEnd: "" };
  for (const row of reportRows(report)) {
    const identity = identities.get(row.campaignId);
    const adamId = row.adamId || identity?.adamId || "";
    const campaignName = row.campaignName || identity?.name || row.campaignId;
    const product = app2product.get(adamId);
    out.campaigns++;
    if (!product) {
      out.unmapped.push({ adamId, campaignName });
      continue;
    }
    for (const d of row.days) {
      const date = String(d.date ?? "").slice(0, 10);
      if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) continue;
      const spend = (d.localSpend ?? d.spend ?? {}) as Record<string, unknown>;
      const rec: CanonicalAdRow = {
        date,
        campaign_id: row.campaignId,
        campaign_name: campaignName,
        spend: String(spend.amount ?? "0"),
        impressions: num(d.impressions),
        clicks: num(d.taps ?? d.clicks),
        conversions: num(d.totalInstalls ?? d.installs ?? d.tapInstalls),
        currency: String(spend.currency ?? "USD"),
      };
      (out.byProduct[product] ??= []).push(rec);
      if (!out.dateStart || date < out.dateStart) out.dateStart = date;
      if (date > out.dateEnd) out.dateEnd = date;
    }
  }
  return out;
}

function csvCell(v: string | number): string {
  const s = String(v);
  return /[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
}

export function writeCsvs(rows: BridgeRows, outDir: string): Record<string, string> {
  mkdirSync(outDir, { recursive: true });
  const paths: Record<string, string> = {};
  for (const [product, recs] of Object.entries(rows.byProduct)) {
    const path = join(outDir, `${product.replace(/^product:/, "").replace(/[^A-Za-z0-9._-]/g, "_")}.csv`);
    const lines = [CANONICAL_HEADERS.join(",")];
    for (const r of recs) lines.push(CANONICAL_HEADERS.map((h) => csvCell(r[h])).join(","));
    writeFileSync(path, lines.join("\n") + "\n");
    paths[product] = path;
  }
  return paths;
}

type Args = {
  config: string;
  reportJson?: string;
  /** The v1 campaign list, when the report does not carry names and app ids. */
  campaignsJson?: string;
  ascCommand?: string;
  /** --org + --days: the tool builds the v1 report request itself and runs `asc`. */
  org?: string;
  days?: number;
  account?: string;
  outDir?: string;
  compoundBin: string;
  apply: boolean;
  json: boolean;
};

export const USAGE =
  "usage: bun tools/apple_ads_daily.ts --config <asc-products.json> (--report-json <report.json> [--campaigns-json <campaigns.json>]|--asc-command <cmd>|--org <ad-account-id> --days <N>) [--account <ad-account-id>] [--out-dir <dir>] [--compound-bin compound] [--apply] [--json]";

/** The v1 app campaign report request for the last `days` days ending today (UTC). */
export function reportPayload(days: number, now: Date = new Date()): Record<string, unknown> {
  if (!Number.isInteger(days) || days < 1 || days > 366) throw new UsageError("--days must be an integer between 1 and 366");
  const end = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate()));
  const start = new Date(end.getTime() - (days - 1) * 86_400_000);
  const day = (d: Date) => d.toISOString().slice(0, 10);
  return {
    timeRange: { start: day(start), end: day(end), timeZone: "UTC", granularity: "DAILY" },
    fields: ["impressions", "taps", "tapInstalls", "viewInstalls", "totalInstalls", "localSpend"],
    pagination: { offset: 0, pageSize: 1000 },
  };
}

/** The v1 campaign list request. */
export function campaignsPayload(): Record<string, unknown> {
  return { pagination: { offset: 0, pageSize: 200 } };
}

/** Proxy variables are dropped: the Apple Ads endpoint is reached directly, and an
 *  inherited loopback proxy has made the CLI fail silently before. The Ads
 *  credentials live in a file, so the keychain branch is skipped: a scheduled
 *  run must never put a prompt on the operator's screen. */
export function ascEnv(base: NodeJS.ProcessEnv = process.env): NodeJS.ProcessEnv {
  const env: NodeJS.ProcessEnv = { ...base };
  for (const k of Object.keys(env)) if (/^(https?_proxy|all_proxy)$/i.test(k)) delete env[k];
  env.ASC_ADS_BYPASS_KEYCHAIN = "1";
  env.ASC_ADS_STRICT_AUTH = "1";
  return env;
}

/** A usage error is thrown, never exited, so parseArgs stays testable. */
export class UsageError extends Error {}

function usage(): never {
  throw new UsageError(USAGE);
}

export function parseArgs(argv: string[]): Args {
  const args: Args = { config: "", compoundBin: "compound", apply: false, json: false };
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === "--apply" || arg === "--json") {
      args[arg === "--apply" ? "apply" : "json"] = true;
      continue;
    }
    const value = argv[i + 1];
    if (value === undefined) usage();
    switch (arg) {
      case "--config":
        args.config = value;
        break;
      case "--report-json":
        args.reportJson = value;
        break;
      case "--campaigns-json":
        args.campaignsJson = value;
        break;
      case "--asc-command":
        args.ascCommand = value;
        break;
      case "--org":
        args.org = value;
        break;
      case "--days":
        args.days = Number(value);
        break;
      case "--account":
        args.account = value;
        break;
      case "--out-dir":
        args.outDir = value;
        break;
      case "--compound-bin":
        args.compoundBin = value;
        break;
      default:
        usage();
    }
    i++;
  }
  const sources = [args.reportJson, args.ascCommand, args.org].filter(Boolean).length;
  if (!args.config || sources !== 1) usage();
  if (args.org && (args.days === undefined || !Number.isInteger(args.days))) usage();
  return args;
}

/** One read-only `asc ads` call with a v1 payload. */
function runAsc(argv: string[], payload: Record<string, unknown>): unknown {
  const dir = mkdtempSync(join(tmpdir(), "apple-ads-daily-"));
  const payloadPath = join(dir, "payload.json");
  writeFileSync(payloadPath, JSON.stringify(payload));
  const result = spawnSync("asc", [...argv, "--file", payloadPath, "--output", "json"], {
    encoding: "utf8",
    maxBuffer: 64 * 1024 * 1024,
    env: ascEnv(),
  });
  if (result.status !== 0) throw new Error(`asc ${argv.join(" ")} failed (${result.status}): ${(result.stderr || "").trim().slice(0, 400)}`);
  return JSON.parse(result.stdout.slice(result.stdout.indexOf("{")));
}

function loadReport(args: Args): { report: unknown; campaigns: unknown } {
  const campaignsFromFile = () => (args.campaignsJson ? JSON.parse(readFileSync(args.campaignsJson, "utf8")) : undefined);
  if (args.reportJson) return { report: JSON.parse(readFileSync(args.reportJson, "utf8")), campaigns: campaignsFromFile() };
  if (args.org) {
    // The campaign list supplies each campaign's name and app; the v1 report
    // carries neither. Both calls are read-only.
    const campaigns = runAsc(["ads", "campaigns", "find", "--ad-account", args.org], campaignsPayload());
    const report = runAsc(["ads", "reports", "apps", "campaigns", "--ad-account", args.org], reportPayload(args.days!));
    return { report, campaigns };
  }
  // The command is the operator's; this tool only runs what it was handed.
  const result = spawnSync("/bin/zsh", ["-lc", args.ascCommand!], { encoding: "utf8", maxBuffer: 64 * 1024 * 1024, env: ascEnv() });
  if (result.status !== 0) throw new Error(`asc command failed (${result.status}): ${(result.stderr || "").trim().slice(0, 400)}`);
  const text = result.stdout;
  return { report: JSON.parse(text.slice(text.indexOf("{"))), campaigns: campaignsFromFile() };
}

export function main(argv: string[]): number {
  const args = parseArgs(argv);
  const config = parseConfig(args.config);
  const { report, campaigns } = loadReport(args);
  if (needsCampaignList(report) && campaigns === undefined) {
    throw new Error(
      "this report carries campaign ids but no app ids: pass the v1 campaign list with --campaigns-json, " +
        "or let the tool read it with --org <ad-account-id> --days <N>",
    );
  }
  const rows = rowsForReport(config, report, campaigns);
  const outDir = args.outDir ?? `${process.env.COMPOUND_HOME ?? `${process.env.HOME}/.compound`}/state/apple-ads-daily`;
  const csvs = writeCsvs(rows, outDir);
  const reportSummary: Record<string, unknown> = {
    schema_version: "compound.tools.apple_ads_daily.v0.1",
    platform: "apple_search_ads",
    campaigns: rows.campaigns,
    date_start: rows.dateStart,
    date_end: rows.dateEnd,
    products: Object.fromEntries(Object.entries(rows.byProduct).map(([p, r]) => [p, { rows: r.length, csv: csvs[p] }])),
    unmapped: rows.unmapped,
    applied: args.apply,
  };
  if (args.apply) {
    const applied: Record<string, unknown>[] = [];
    for (const [product, csv] of Object.entries(csvs)) {
      const cmd = ["product", "ads", "import", "--platform", "apple_search_ads", "--product", product, "--report", csv, "--json"];
      if (args.account) cmd.push("--account", args.account);
      const result = spawnSync(args.compoundBin, cmd, { encoding: "utf8" });
      let summary: unknown = null;
      try {
        summary = JSON.parse(result.stdout.slice(result.stdout.indexOf("{")))?.import ?? null;
      } catch {
        summary = null;
      }
      applied.push({ product, status: result.status, import: summary, stderr: (result.stderr || "").trim().slice(0, 300) });
      if (result.status !== 0) reportSummary.status = "failed";
    }
    reportSummary.apply_results = applied;
  }
  reportSummary.status ??= rows.unmapped.length > 0 ? "partial" : "ok";
  if (args.json) console.log(JSON.stringify(reportSummary, null, 2));
  else {
    console.log(`# apple ads daily (${rows.dateStart}..${rows.dateEnd})`);
    console.log(`- campaigns: ${rows.campaigns} · unmapped: ${rows.unmapped.length} · applied: ${args.apply}`);
    for (const [p, csv] of Object.entries(csvs)) console.log(`- ${p}: ${rows.byProduct[p].length} rows → ${csv}`);
    for (const u of rows.unmapped) console.log(`- unmapped adamId ${u.adamId} (${u.campaignName}): add it to the private asc-products config`);
  }
  return reportSummary.status === "failed" ? 1 : 0;
}

if (import.meta.main) {
  try {
    process.exit(main(process.argv.slice(2)));
  } catch (err) {
    if (err instanceof UsageError) {
      console.error(err.message);
      process.exit(2);
    }
    console.error(`apple_ads_daily: ${err instanceof Error ? err.message : String(err)}`);
    process.exit(1);
  }
}
