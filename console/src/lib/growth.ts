import { runProjection, type ProjectionStatus } from "@/lib/compound";

/*
 * App growth intelligence — read model for `compound product growth --json`
 * (schema compound.product_growth.v0.1). The CLI owns every number and every
 * status; this file only maps snake_case JSON into typed rows and adds display
 * helpers. Nothing here decides whether an area is measured or blind.
 */

export const GROWTH_COMMAND = "compound product growth --json";

export type GrowthStatus = "measured" | "stale" | "partial" | "blind" | "not_applicable";
export type FocusClass = "grow" | "fix_data" | "measure" | "hold";
export type KpiKey =
  | "proceeds_usd"
  | "units"
  | "first_time_downloads"
  | "web_clicks"
  | "web_impressions"
  | "ad_spend_usd"
  | "ad_conversions"
  | "ad_cpa_usd";
export type SeriesKey = "proceeds_usd" | "units" | "web_clicks" | "ad_spend_usd" | "ad_conversions";

export type Kpi = {
  value: number | null;
  previous: number | null;
  hasData: boolean;
  latestDate: string;
  daysReported: number;
  daysInWindow: number;
  unit: "usd" | "count" | "ratio" | string;
};

export type SeriesPoint = { date: string; value: number | null };

export type GrowthStage = { id: string; title: string; question: string };
export type GrowthArea = {
  id: string;
  number: number;
  stage: string;
  title: string;
  question: string;
  inputs: string[];
  appliesTo: string[];
};

export type AuditRow = {
  areaId: string;
  status: GrowthStatus;
  summary: string;
  evidence: { metric: string; rows: number; latestDate: string }[];
  nextMove: string;
};

export type FunnelStep = {
  id: string;
  label: string;
  value: number | null;
  asOf: string;
  status: GrowthStatus;
  note: string;
};

export type NextMove = { areaId: string; kind: string; title: string; why: string };

export type SourceFreshness = { source: string; latestDate: string; daysSince: number; stale: boolean };

export type GrowthApp = {
  productId: string;
  name: string;
  aliases: string[];
  kind: "ios_app" | "web" | "unknown" | string;
  essence: { audience: string; problem: string; value: string } | null;
  freshness: { latestDate: string; stale: boolean; sources: SourceFreshness[] };
  kpis: Partial<Record<KpiKey, Kpi>>;
  series: Partial<Record<SeriesKey, SeriesPoint[]>>;
  funnel: FunnelStep[];
  audit: AuditRow[];
  coverage: { measured: number; partial: number; stale: number; blind: number; notApplicable: number; score: number };
  hypotheses: { open: number; reviewed: number };
  nextMoves: NextMove[];
};

export type Allocation = { productId: string; name: string; rank: number; focus: FocusClass | string; reason: string; rule: string };

export type GrowthModel = {
  generatedAt: string;
  asOf: string;
  windowDays: number;
  seriesDays: number;
  staleDays: number;
  boundary: string;
  stages: GrowthStage[];
  areas: GrowthArea[];
  portfolio: {
    appCount: number;
    appsWithoutData: string[];
    totals: Partial<Record<KpiKey, Kpi>>;
    series: Partial<Record<SeriesKey, SeriesPoint[]>>;
    allocation: Allocation[];
  };
  apps: GrowthApp[];
};

export type GrowthRead = {
  status: ProjectionStatus;
  model: GrowthModel | null;
  error: string | null;
};

type Raw = Record<string, unknown>;
const rec = (v: unknown): Raw => (v && typeof v === "object" && !Array.isArray(v) ? (v as Raw) : {});
const list = (v: unknown): unknown[] => (Array.isArray(v) ? v : []);
const s = (v: unknown): string => (typeof v === "string" ? v : "");
const n = (v: unknown): number => (typeof v === "number" && Number.isFinite(v) ? v : 0);
const nOrNull = (v: unknown): number | null => (typeof v === "number" && Number.isFinite(v) ? v : null);

function mapKpi(v: unknown): Kpi {
  const o = rec(v);
  return {
    value: nOrNull(o.value),
    previous: nOrNull(o.previous),
    hasData: o.has_data === true,
    latestDate: s(o.latest_date),
    daysReported: n(o.days_reported),
    daysInWindow: n(o.days_in_window),
    unit: s(o.unit),
  };
}

function mapKpis(v: unknown): Partial<Record<KpiKey, Kpi>> {
  const out: Partial<Record<KpiKey, Kpi>> = {};
  for (const [key, value] of Object.entries(rec(v))) out[key as KpiKey] = mapKpi(value);
  return out;
}

function mapSeries(v: unknown): Partial<Record<SeriesKey, SeriesPoint[]>> {
  const out: Partial<Record<SeriesKey, SeriesPoint[]>> = {};
  for (const [key, points] of Object.entries(rec(v))) {
    out[key as SeriesKey] = list(points).map((p) => ({ date: s(rec(p).date), value: nOrNull(rec(p).value) }));
  }
  return out;
}

function mapApp(v: unknown): GrowthApp {
  const o = rec(v);
  const essence = rec(o.essence);
  const fresh = rec(o.freshness);
  const cov = rec(o.coverage);
  const hyp = rec(o.hypotheses);
  return {
    productId: s(o.product_id),
    name: s(o.name) || s(o.product_id).replace(/^product:/, ""),
    aliases: list(o.aliases).map(s).filter(Boolean),
    kind: s(o.kind) || "unknown",
    essence: o.essence ? { audience: s(essence.audience), problem: s(essence.problem), value: s(essence.value) } : null,
    freshness: {
      latestDate: s(fresh.latest_date),
      stale: fresh.stale === true,
      sources: list(fresh.sources).map((x) => {
        const r = rec(x);
        return { source: s(r.source), latestDate: s(r.latest_date), daysSince: n(r.days_since), stale: r.stale === true };
      }),
    },
    kpis: mapKpis(o.kpis),
    series: mapSeries(o.series),
    funnel: list(o.funnel).map((x) => {
      const r = rec(x);
      return { id: s(r.id), label: s(r.label), value: nOrNull(r.value), asOf: s(r.as_of), status: (s(r.status) || "blind") as GrowthStatus, note: s(r.note) };
    }),
    audit: list(o.audit).map((x) => {
      const r = rec(x);
      return {
        areaId: s(r.area_id),
        status: (s(r.status) || "blind") as GrowthStatus,
        summary: s(r.summary),
        evidence: list(r.evidence).map((e) => ({ metric: s(rec(e).metric), rows: n(rec(e).rows), latestDate: s(rec(e).latest_date) })),
        nextMove: s(r.next_move),
      };
    }),
    coverage: {
      measured: n(cov.measured),
      partial: n(cov.partial),
      stale: n(cov.stale),
      blind: n(cov.blind),
      notApplicable: n(cov.not_applicable),
      score: n(cov.score),
    },
    hypotheses: { open: n(hyp.open), reviewed: n(hyp.reviewed) },
    nextMoves: list(o.next_moves).map((x) => {
      const r = rec(x);
      return { areaId: s(r.area_id), kind: s(r.kind), title: s(r.title), why: s(r.why) };
    }),
  };
}

export function mapGrowthModel(j: unknown): GrowthModel | null {
  const o = rec(j);
  if (!s(o.schema_version).startsWith("compound.product_growth.")) return null;
  const p = rec(o.portfolio);
  return {
    generatedAt: s(o.generated_at),
    asOf: s(o.as_of),
    windowDays: n(o.window_days) || 30,
    seriesDays: n(o.series_days) || 120,
    staleDays: n(o.stale_days) || 14,
    boundary: s(o.boundary),
    stages: list(o.stages).map((x) => ({ id: s(rec(x).id), title: s(rec(x).title), question: s(rec(x).question) })),
    areas: list(o.areas).map((x) => {
      const r = rec(x);
      return {
        id: s(r.id),
        number: n(r.number),
        stage: s(r.stage),
        title: s(r.title),
        question: s(r.question),
        inputs: list(r.inputs).map(s),
        appliesTo: list(r.applies_to).map(s),
      };
    }),
    portfolio: {
      appCount: n(p.app_count),
      appsWithoutData: list(p.apps_without_data).map(s),
      totals: mapKpis(p.totals),
      series: mapSeries(p.series),
      allocation: list(p.allocation).map((x) => {
        const r = rec(x);
        return { productId: s(r.product_id), name: s(r.name), rank: n(r.rank), focus: s(r.focus), reason: s(r.reason), rule: s(r.rule) };
      }),
    },
    apps: list(o.apps).map(mapApp),
  };
}

/** One shared read; every growth surface (sidebar, portfolio, audit, app page) uses the same argv so the TTL cache serves them all. */
export async function getGrowth(): Promise<GrowthRead> {
  const read = await runProjection(["product", "growth", "--json"], { timeoutMs: 60_000 });
  return { status: read.status, model: mapGrowthModel(read.value), error: read.error };
}

/* ---------------- identity ---------------- */

export { appHue, appSlug, humanizeProductId } from "@/lib/app-identity";

export function findApp(model: GrowthModel, slug: string): GrowthApp | undefined {
  const wanted = decodeURIComponent(slug);
  const id = wanted.startsWith("product:") ? wanted : `product:${wanted}`;
  return model.apps.find((a) => a.productId === id || a.aliases.includes(id));
}

/* ---------------- status vocabulary (one place) ---------------- */

/**
 * `dot` is the small status mark; `fill` paints larger areas (matrix cells,
 * bar segments). Blind fills are deliberately soft: on a portfolio that is
 * mostly blind, what IS known must stand out, and absence must stay visible
 * without shouting.
 */
export const STATUS_META: Record<GrowthStatus, { label: string; short: string; dot: string; fill: string; chip: string; meaning: string }> = {
  measured: {
    label: "Measured",
    short: "Measured",
    dot: "bg-good",
    fill: "bg-good",
    chip: "border-good-line bg-good-soft text-good",
    meaning: "Fresh data, enough points to read a trend.",
  },
  partial: {
    label: "Partial",
    short: "Partial",
    dot: "bg-work",
    fill: "bg-work",
    chip: "border-work-line bg-work-soft text-work",
    meaning: "Some data, but a piece needed to decide is missing.",
  },
  stale: {
    label: "Stale",
    short: "Stale",
    dot: "bg-seam",
    fill: "bg-seam",
    chip: "border-seam-line bg-seam-soft text-seam",
    meaning: "Data exists but stopped arriving.",
  },
  blind: {
    label: "Blind",
    short: "Blind",
    dot: "bg-crit",
    fill: "border border-crit-line bg-crit-soft",
    chip: "border-crit-line bg-crit-soft text-crit",
    meaning: "Nothing measures this today.",
  },
  not_applicable: {
    label: "Not applicable",
    short: "N/A",
    dot: "bg-hair",
    fill: "border border-dashed border-hair",
    chip: "border-hair bg-soft text-faint",
    meaning: "Does not apply to this kind of app.",
  },
};

/** Segment colour for thin stacked bars (a border would swallow a 2px bar, so blind uses its soft line hue). */
export function barClass(status: GrowthStatus): string {
  if (status === "blind") return "bg-crit-line";
  if (status === "not_applicable") return "bg-hair";
  return STATUS_META[status].dot;
}

export const FOCUS_META: Record<string, { label: string; chip: string; meaning: string }> = {
  grow: { label: "Grow", chip: "border-good-line bg-good-soft text-good", meaning: "Earning, with fresh data. Improve the funnel." },
  fix_data: { label: "Fix data first", chip: "border-seam-line bg-seam-soft text-seam", meaning: "Earning, but the sales data stopped. Refresh before deciding." },
  measure: { label: "Measure", chip: "border-work-line bg-work-soft text-work", meaning: "Has traffic, but the money side is blind." },
  hold: { label: "Hold", chip: "border-hair bg-soft text-muted", meaning: "Little revenue or traffic. Not worth time now." },
};

export const KPI_META: Record<KpiKey, { label: string; hint: string }> = {
  proceeds_usd: { label: "Proceeds", hint: "App Store developer proceeds" },
  units: { label: "Units", hint: "App Store units (downloads + purchases)" },
  first_time_downloads: { label: "First-time downloads", hint: "New installs" },
  web_clicks: { label: "Web search clicks", hint: "Google Search Console clicks" },
  web_impressions: { label: "Web search impressions", hint: "Google Search Console impressions" },
  ad_spend_usd: { label: "Ad spend", hint: "Apple Search Ads spend" },
  ad_conversions: { label: "Ad installs", hint: "Apple Search Ads conversions" },
  ad_cpa_usd: { label: "Cost per install", hint: "Ad spend ÷ ad installs" },
};

/* ---------------- number formatting ---------------- */

export function formatKpi(kpi: Kpi | undefined, opts?: { compact?: boolean }): string {
  if (!kpi || kpi.value == null) return "—";
  return formatValue(kpi.value, kpi.unit, opts);
}

export function formatValue(value: number, unit: string, opts?: { compact?: boolean }): string {
  if (unit === "usd") {
    if (opts?.compact && Math.abs(value) >= 1000) return `$${(value / 1000).toFixed(1)}k`;
    return `$${value.toLocaleString("en-US", { minimumFractionDigits: value < 100 ? 2 : 0, maximumFractionDigits: value < 100 ? 2 : 0 })}`;
  }
  if (unit === "ratio") return `${(value * 100).toFixed(1)}%`;
  if (opts?.compact && Math.abs(value) >= 10_000) return `${(value / 1000).toFixed(1)}k`;
  return Math.round(value).toLocaleString("en-US");
}

/** Percent change vs previous window; null when either side is missing or the base is zero. */
export function kpiDelta(kpi: Kpi | undefined): number | null {
  if (!kpi || kpi.value == null || kpi.previous == null || kpi.previous === 0) return null;
  return (kpi.value - kpi.previous) / Math.abs(kpi.previous);
}

/** "Lower is better" KPIs flip the delta colour. */
export function kpiLowerIsBetter(key: KpiKey): boolean {
  return key === "ad_cpa_usd" || key === "ad_spend_usd";
}

export function shortDate(iso: string): string {
  if (!iso) return "";
  const d = new Date(`${iso.slice(0, 10)}T00:00:00Z`);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleDateString("en-US", { month: "short", day: "numeric", timeZone: "UTC" });
}
