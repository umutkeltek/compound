import { runProjection, type ProjectionStatus } from "@/lib/compound";

/*
 * App growth intelligence — read model for `compound product growth --json`
 * (schema compound.product_growth.v0.2). The CLI owns every number, every
 * status, every applicability call and the order of next moves; this file only
 * maps snake_case JSON into typed rows and adds display words. Nothing here
 * decides whether an area is measured, applies, or comes first.
 */

export const GROWTH_COMMAND = "compound product growth --json";

export type GrowthStatus = "measured" | "partial" | "stale" | "blind" | "not_applicable" | "unknown" | "deferred";
export type FocusClass = "grow" | "fix_data" | "measure" | "hold";
export type KpiKey =
  | "proceeds_usd"
  | "units"
  | "first_time_downloads"
  | "web_clicks"
  | "web_impressions"
  | "ad_spend_usd"
  | "ad_conversions"
  | "ad_cpa_usd"
  | "iap_units"
  | "updates"
  | "redownloads";
export type SeriesKey = "proceeds_usd" | "units" | "web_clicks" | "ad_spend_usd" | "ad_conversions";

export type Kpi = {
  value: number | null;
  previous: number | null;
  hasData: boolean;
  latestDate: string;
  daysReported: number;
  daysInWindow: number;
  unit: "usd" | "count" | "ratio" | string;
  note?: string;
  /** First and last day the value sums ("" from a CLI that does not report them). */
  windowStart: string;
  windowEnd: string;
};

export type SeriesPoint = { date: string; value: number | null };

/** One of the 12 catalog groups (opportunity … operations). Not the product group of aliases. */
export type GrowthGroup = { id: string; title: string; question: string; branch: string };

/** A per-kind form of an area ("App Store search" for an iPhone app, "Search engines" for a website). */
export type AreaForm = { title: string; question: string; inputs: string[]; skills: string[] };

export type GrowthArea = {
  id: string;
  number: number;
  group: string;
  title: string;
  question: string;
  inputs: string[];
  /** Profile dimension → allowed values. Empty means the area applies to every product. */
  applies: Record<string, string[]>;
  needs: string[];
  branch: string;
  skills: string[];
  kinds: Record<string, AreaForm>;
};

export type AuditBasis = "metric" | "record" | "assessment" | "mixed" | "none" | string;

export type AuditRow = {
  areaId: string;
  status: GrowthStatus;
  basis: AuditBasis;
  /** Title and question resolved for this product (the form title when exactly one form applies). */
  title: string;
  question: string;
  /** Form titles in play; empty when only the base area applies. */
  forms: string[];
  summary: string;
  /** Set for not_applicable, unknown and deferred. */
  reason: string;
  evidence: { metric: string; rows: number; latestDate: string }[];
  nextMove: string;
  /** Area ids this area needs first that have no data yet or are not on file. */
  blockedBy: string[];
  branch: string;
  skills: string[];
};

export type FunnelStep = {
  id: string;
  label: string;
  value: number | null;
  asOf: string;
  status: GrowthStatus;
  note: string;
  /** The days a window total sums; "" when the value is one reading or the CLI does not report it. */
  windowStart: string;
  windowEnd: string;
  /** Days of that window with data; 0 when not reported. */
  daysReported: number;
};

export type NextMoveKind =
  | "refresh_data"
  | "set_profile"
  | "connect_data"
  | "record_answer"
  | "assess"
  | "review_hypothesis"
  | "run_experiment";

export type NextMove = {
  /** Empty for the one set_profile move. */
  areaId: string;
  /** Every area the move covers, in catalog order: one for most moves, several for a folded refresh, none for set_profile. */
  areaIds: string[];
  kind: NextMoveKind | string;
  title: string;
  why: string;
  /** How many applicable areas wait on this one (transitive). */
  unblocks: number;
  blockedBy: string[];
  branch: string;
  skills: string[];
  /** The command an agent or skill runs to start this move. */
  brief: string;
};

export type SourceFreshness = { source: string; latestDate: string; daysSince: number; stale: boolean };

export type ProfileDimension = "kinds" | "audiences" | "revenue_models" | "objectives" | "stage";
export const PROFILE_DIMENSIONS: ProfileDimension[] = ["kinds", "audiences", "revenue_models", "objectives", "stage"];

export type ProfileValue = { values: string[]; source: "declared" | "inferred" | "unknown" | string };
export type Deferral = { area: string; reason: string; until: string };

export type GrowthProfile = Record<ProfileDimension, ProfileValue> & {
  deferred: Deferral[];
  /** Dimensions the profile does not say. */
  unknown: ProfileDimension[];
};

export type Coverage = {
  measured: number;
  partial: number;
  stale: number;
  blind: number;
  notApplicable: number;
  unknown: number;
  deferred: number;
  score: number;
};

export type GrowthApp = {
  productId: string;
  name: string;
  aliases: string[];
  /** The id the app's group is known by in other records ("" from a CLI that does not send `group_id`). */
  groupId: string;
  /** First profile kind or "unknown" (kept for the sidebar and older readers). */
  kind: string;
  essence: { audience: string; problem: string; value: string } | null;
  freshness: { latestDate: string; stale: boolean; sources: SourceFreshness[] };
  kpis: Partial<Record<KpiKey, Kpi>>;
  series: Partial<Record<SeriesKey, SeriesPoint[]>>;
  funnel: FunnelStep[];
  profile: GrowthProfile;
  audit: AuditRow[];
  coverage: Coverage;
  hypotheses: { open: number; reviewed: number };
  nextMoves: NextMove[];
};

export type Allocation = {
  productId: string;
  name: string;
  rank: number;
  focus: FocusClass | string;
  reason: string;
  rule: string;
  /** The days of the 30-day proceeds in `reason`; "" from a CLI whose reason may use another window than the tile. */
  windowStart: string;
  windowEnd: string;
};

export type CatalogInfo = {
  schemaVersion: string;
  source: string;
  overlay: string;
  overlayError: string;
  groups: number;
  areas: number;
};

export type GrowthModel = {
  schemaVersion: string;
  generatedAt: string;
  asOf: string;
  windowDays: number;
  seriesDays: number;
  staleDays: number;
  assessDays: number;
  boundary: string;
  catalog: CatalogInfo;
  profileDimensions: Partial<Record<ProfileDimension, string[]>>;
  groups: GrowthGroup[];
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
const strs = (v: unknown): string[] => list(v).map(s).filter(Boolean);
const n = (v: unknown): number => (typeof v === "number" && Number.isFinite(v) ? v : 0);
const nOrNull = (v: unknown): number | null => (typeof v === "number" && Number.isFinite(v) ? v : null);

const KNOWN_STATUSES = new Set<GrowthStatus>(["measured", "partial", "stale", "blind", "not_applicable", "unknown", "deferred"]);
/** An unrecognised status is shown as "no data yet", never as a better state than the CLI said. */
const status = (v: unknown): GrowthStatus => (KNOWN_STATUSES.has(s(v) as GrowthStatus) ? (s(v) as GrowthStatus) : "blind");

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
    note: s(o.note),
    windowStart: s(o.window_start),
    windowEnd: s(o.window_end),
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

function mapProfileValue(v: unknown): ProfileValue {
  const o = rec(v);
  const values = strs(o.values);
  return { values, source: s(o.source) || (values.length ? "declared" : "unknown") };
}

function mapProfile(v: unknown): GrowthProfile {
  const o = rec(v);
  const profile = {
    kinds: mapProfileValue(o.kinds),
    audiences: mapProfileValue(o.audiences),
    revenue_models: mapProfileValue(o.revenue_models),
    objectives: mapProfileValue(o.objectives),
    stage: mapProfileValue(o.stage),
    deferred: list(o.deferred).map((x) => {
      const r = rec(x);
      return { area: s(r.area), reason: s(r.reason), until: s(r.until) };
    }),
    unknown: strs(o.unknown).filter((d): d is ProfileDimension => (PROFILE_DIMENSIONS as string[]).includes(d)),
  };
  return profile;
}

function mapAudit(x: unknown): AuditRow {
  const r = rec(x);
  return {
    areaId: s(r.area_id),
    status: status(r.status),
    basis: s(r.basis) || "none",
    title: s(r.title),
    question: s(r.question),
    forms: strs(r.forms),
    summary: s(r.summary),
    reason: s(r.reason),
    evidence: list(r.evidence).map((e) => ({ metric: s(rec(e).metric), rows: n(rec(e).rows), latestDate: s(rec(e).latest_date) })),
    nextMove: s(r.next_move),
    blockedBy: strs(r.blocked_by),
    branch: s(r.branch),
    skills: strs(r.skills),
  };
}

function mapNextMove(x: unknown): NextMove {
  const r = rec(x);
  const areaId = s(r.area_id);
  const areaIds = strs(r.area_ids);
  return {
    areaId,
    // A CLI without area_ids still names its one area.
    areaIds: areaIds.length ? areaIds : areaId ? [areaId] : [],
    kind: s(r.kind),
    title: s(r.title),
    why: s(r.why),
    unblocks: n(r.unblocks),
    blockedBy: strs(r.blocked_by),
    branch: s(r.branch),
    skills: strs(r.skills),
    brief: s(r.brief),
  };
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
    aliases: strs(o.aliases),
    groupId: s(o.group_id),
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
      return {
        id: s(r.id),
        label: s(r.label),
        value: nOrNull(r.value),
        asOf: s(r.as_of),
        status: status(r.status),
        note: s(r.note),
        windowStart: s(r.window_start),
        windowEnd: s(r.window_end),
        daysReported: n(r.days_reported),
      };
    }),
    profile: mapProfile(o.profile),
    audit: list(o.audit).map(mapAudit),
    coverage: {
      measured: n(cov.measured),
      partial: n(cov.partial),
      stale: n(cov.stale),
      blind: n(cov.blind),
      notApplicable: n(cov.not_applicable),
      unknown: n(cov.unknown),
      deferred: n(cov.deferred),
      score: n(cov.score),
    },
    hypotheses: { open: n(hyp.open), reviewed: n(hyp.reviewed) },
    nextMoves: list(o.next_moves).map(mapNextMove),
  };
}

function mapArea(x: unknown): GrowthArea {
  const r = rec(x);
  const applies: Record<string, string[]> = {};
  for (const [dim, values] of Object.entries(rec(r.applies))) applies[dim] = strs(values);
  const kinds: Record<string, AreaForm> = {};
  for (const [kind, form] of Object.entries(rec(r.kinds))) {
    const f = rec(form);
    kinds[kind] = { title: s(f.title), question: s(f.question), inputs: strs(f.inputs), skills: strs(f.skills) };
  }
  return {
    id: s(r.id),
    number: n(r.number),
    group: s(r.group),
    title: s(r.title),
    question: s(r.question),
    inputs: strs(r.inputs),
    applies,
    needs: strs(r.needs),
    branch: s(r.branch),
    skills: strs(r.skills),
    kinds,
  };
}

/** Accepts only v0.2 and later: v0.1 had `stages` and no profile, and would render as an empty catalog. */
export function mapGrowthModel(j: unknown): GrowthModel | null {
  const o = rec(j);
  const version = s(o.schema_version);
  if (!version.startsWith("compound.product_growth.") || version === "compound.product_growth.v0.1") return null;
  const p = rec(o.portfolio);
  const cat = rec(o.catalog);
  const dims = rec(o.profile_dimensions);
  const profileDimensions: Partial<Record<ProfileDimension, string[]>> = {};
  for (const [dim, values] of Object.entries(dims)) profileDimensions[dim as ProfileDimension] = strs(values);
  return {
    schemaVersion: version,
    generatedAt: s(o.generated_at),
    asOf: s(o.as_of),
    windowDays: n(o.window_days) || 30,
    seriesDays: n(o.series_days) || 120,
    staleDays: n(o.stale_days) || 14,
    assessDays: n(o.assess_days) || 180,
    boundary: s(o.boundary),
    catalog: {
      schemaVersion: s(cat.schema_version),
      source: s(cat.source),
      overlay: s(cat.overlay),
      overlayError: s(cat.overlay_error),
      groups: n(cat.groups),
      areas: n(cat.areas),
    },
    profileDimensions,
    groups: list(o.groups).map((x) => {
      const r = rec(x);
      return { id: s(r.id), title: s(r.title), question: s(r.question), branch: s(r.branch) };
    }),
    areas: list(o.areas).map(mapArea),
    portfolio: {
      appCount: n(p.app_count),
      appsWithoutData: strs(p.apps_without_data),
      totals: mapKpis(p.totals),
      series: mapSeries(p.series),
      allocation: list(p.allocation).map((x) => {
        const r = rec(x);
        return {
          productId: s(r.product_id),
          name: s(r.name),
          rank: n(r.rank),
          focus: s(r.focus),
          reason: s(r.reason),
          rule: s(r.rule),
          windowStart: s(r.window_start),
          windowEnd: s(r.window_end),
        };
      }),
    },
    apps: list(o.apps).map(mapApp),
  };
}

/** One shared read; every growth surface (sidebar, portfolio, audit, app page) uses the same argv so the TTL cache serves them all. */
export async function getGrowth(): Promise<GrowthRead> {
  const read = await runProjection(["product", "growth", "--json"], { timeoutMs: 60_000 });
  const model = mapGrowthModel(read.value);
  const error =
    read.error ??
    (read.value && !model ? `compound returned ${String(rec(read.value).schema_version || "an unknown schema")}; this console reads compound.product_growth.v0.2` : null);
  return { status: read.status, model, error };
}

/* ---------------- identity ---------------- */

export { appHue, appSlug, humanizeProductId } from "@/lib/app-identity";

export function findApp(model: GrowthModel, slug: string): GrowthApp | undefined {
  const wanted = decodeURIComponent(slug);
  const id = wanted.startsWith("product:") ? wanted : `product:${wanted}`;
  return model.apps.find((a) => a.productId === id || a.aliases.includes(id));
}

/* ---------------- status vocabulary (one place) ---------------- */

/** Display order: what is known first, then the calm gaps. */
export const STATUS_ORDER: GrowthStatus[] = ["measured", "partial", "stale", "blind", "unknown", "deferred", "not_applicable"];

/** Statuses that count toward "applicable" (the coverage denominator the CLI uses). */
export function isJudged(st: GrowthStatus): boolean {
  return st === "measured" || st === "partial" || st === "stale" || st === "blind";
}

/** Any evidence on file at all, fresh or not. */
export function hasEvidence(st: GrowthStatus): boolean {
  return st === "measured" || st === "partial" || st === "stale";
}

export function emptyCounts(): Record<GrowthStatus, number> {
  return { measured: 0, partial: 0, stale: 0, blind: 0, not_applicable: 0, unknown: 0, deferred: 0 };
}

export function countStatuses(rows: { status: GrowthStatus }[]): Record<GrowthStatus, number> {
  const out = emptyCounts();
  for (const row of rows) out[row.status] += 1;
  return out;
}

/**
 * Words and colours for each status. `fill` paints one area's cell, `bar` one
 * segment of a thin stacked bar. "No data yet" is deliberately the calmest
 * filled state: with 120 areas and little data most cells are gaps, so gaps
 * are background and the few measured, out-of-date and next-move cells stand
 * out. "Not on file", "Set aside" and "Does not apply" are calmer still.
 */
export const STATUS_META: Record<GrowthStatus, { label: string; dot: string; fill: string; bar: string; chip: string; meaning: string }> = {
  measured: {
    label: "Measured",
    dot: "bg-good",
    fill: "bg-good",
    bar: "bg-good",
    chip: "border-good-line bg-good-soft text-good",
    meaning: "Fresh evidence answers this question.",
  },
  partial: {
    label: "Partly measured",
    dot: "bg-work",
    fill: "bg-work-line",
    bar: "bg-work-line",
    chip: "border-work-line bg-work-soft text-work",
    meaning: "Some evidence, but a piece needed to decide is missing.",
  },
  stale: {
    label: "Out of date",
    dot: "bg-seam",
    fill: "bg-seam",
    bar: "bg-seam",
    chip: "border-seam-line bg-seam-soft text-seam",
    meaning: "Evidence exists, but it stopped arriving.",
  },
  blind: {
    label: "No data yet",
    dot: "bg-faint",
    fill: "bg-soft-2",
    bar: "bg-hair",
    chip: "border-hair bg-soft text-muted",
    meaning: "Nothing measures this today.",
  },
  unknown: {
    label: "Not on file",
    dot: "border border-dashed border-faint",
    fill: "border border-dashed border-hair bg-panel",
    bar: "hatch bg-panel text-faint",
    chip: "border-dashed border-hair bg-panel text-muted",
    meaning: "We cannot tell if this applies until the product profile says more.",
  },
  deferred: {
    label: "Set aside",
    dot: "bg-hair",
    fill: "hatch bg-soft text-hair",
    bar: "bg-hair-2",
    chip: "border-hair bg-soft text-faint",
    meaning: "The owner set this aside for now.",
  },
  not_applicable: {
    label: "Does not apply",
    dot: "bg-hair-2",
    fill: "",
    bar: "",
    chip: "border-hair-2 bg-panel text-faint",
    meaning: "Does not apply to this product.",
  },
};

/** Segment colour for thin stacked bars. */
export function barClass(st: GrowthStatus): string {
  return STATUS_META[st].bar;
}

/** Plain words for next-move kinds (the CLI's order is the claim; this is only the label). */
export const MOVE_KIND_LABEL: Record<string, string> = {
  refresh_data: "Refresh data",
  set_profile: "Write the profile",
  connect_data: "Connect data",
  record_answer: "Record the answer",
  assess: "Assess",
  review_hypothesis: "Review a hypothesis",
  run_experiment: "Run an experiment",
};

export const DIMENSION_META: Record<ProfileDimension, { label: string; phrase: string }> = {
  kinds: { label: "Kind", phrase: "the kind of product" },
  audiences: { label: "Audience", phrase: "the audience" },
  revenue_models: { label: "Revenue model", phrase: "the revenue model" },
  objectives: { label: "Objective", phrase: "the objective" },
  stage: { label: "Stage", phrase: "the stage" },
};

const VALUE_LABEL: Record<string, string> = {
  ios_app: "iPhone app",
  android_app: "Android app",
  web_app: "Web app",
  website: "Website",
  api: "API",
  dev_tool: "Developer tool",
  browser_extension: "Browser extension",
  digital_download: "Digital download",
  marketplace: "Marketplace",
  ai_agent: "AI agent",
  internal_tool: "Internal tool",
  consumer: "Consumers",
  business: "Businesses",
  developer: "Developers",
  internal: "Internal users",
  one_time: "One-time purchase",
  subscription: "Subscription",
  usage: "Usage-based",
  ads: "Ads",
  licensing: "Licensing",
  transactions: "Transactions",
  enterprise: "Enterprise deals",
  none: "No revenue",
  revenue: "Revenue",
  adoption: "Adoption",
  hours_saved: "Hours saved",
  learning: "Learning",
};

/** "ios_app" → "iPhone app"; unknown values are humanised, never hidden. */
export function valueLabel(v: string): string {
  if (VALUE_LABEL[v]) return VALUE_LABEL[v];
  const t = v.replace(/[_-]+/g, " ").trim();
  return t ? t.charAt(0).toUpperCase() + t.slice(1) : v;
}

const BRANCH_LABEL: Record<string, string> = { ux: "UX", ui: "UI", qa: "QA" };

export function branchLabel(branch: string): string {
  if (!branch) return "";
  return BRANCH_LABEL[branch] ?? branch.charAt(0).toUpperCase() + branch.slice(1);
}

/** "a, b and c" */
export function joinWords(words: string[]): string {
  if (words.length <= 1) return words[0] ?? "";
  return `${words.slice(0, -1).join(", ")} and ${words[words.length - 1]}`;
}

/**
 * For the "Not on file" rows of one app: which unknown profile dimensions keep
 * them from being judged. Read from the catalog's `applies` rules, so the
 * sentence names only dimensions that really gate an area.
 */
export function unknownGates(app: GrowthApp, areas: GrowthArea[]): { rows: number; dims: ProfileDimension[] } {
  const byId = new Map(areas.map((a) => [a.id, a]));
  const unknownDims = new Set(app.profile.unknown);
  const dims = new Set<ProfileDimension>();
  let rows = 0;
  for (const row of app.audit) {
    if (row.status !== "unknown") continue;
    rows += 1;
    for (const dim of Object.keys(byId.get(row.areaId)?.applies ?? {})) {
      if (unknownDims.has(dim as ProfileDimension)) dims.add(dim as ProfileDimension);
    }
  }
  return { rows, dims: PROFILE_DIMENSIONS.filter((d) => dims.has(d)) };
}

export const FOCUS_META: Record<string, { label: string; chip: string; meaning: string }> = {
  grow: { label: "Grow", chip: "border-good-line bg-good-soft text-good", meaning: "Earning, with fresh data. Improve the funnel." },
  fix_data: { label: "Fix data first", chip: "border-seam-line bg-seam-soft text-seam", meaning: "Earning, but the sales data stopped. Refresh before deciding." },
  measure: { label: "Measure", chip: "border-work-line bg-work-soft text-work", meaning: "Has traffic, but the money side has no data yet." },
  hold: { label: "Hold", chip: "border-hair bg-soft text-muted", meaning: "Little revenue or traffic. Not worth time now." },
};

export const KPI_META: Record<KpiKey, { label: string; hint: string }> = {
  proceeds_usd: { label: "Proceeds", hint: "App Store developer proceeds (estimated in US dollars)" },
  units: { label: "New downloads", hint: "New downloads in the Sales report (no redownloads, updates or in-app purchases)" },
  first_time_downloads: { label: "First-time downloads", hint: "App Store analytics (noised)" },
  web_clicks: { label: "Web search clicks", hint: "Google Search Console clicks" },
  web_impressions: { label: "Web search impressions", hint: "Google Search Console impressions" },
  ad_spend_usd: { label: "Ad spend", hint: "What the ads cost, as the ad platform reports it" },
  ad_conversions: { label: "Ad installs", hint: "Installs the ad platform credits to ads. Apple counts re-downloads too, so this can exceed new downloads." },
  ad_cpa_usd: { label: "Cost per install", hint: "Ad spend ÷ ad installs, over the same days" },
  iap_units: { label: "In-app purchases", hint: "In-app purchase units in the Sales report" },
  updates: { label: "Updates", hint: "App updates in the Sales report" },
  redownloads: { label: "Redownloads", hint: "Downloads by people who had the app before" },
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

/** "Oct 15 – Nov 13"; one date when both ends are the same day; "" when either is missing. */
export function dateRange(start: string, end: string): string {
  if (!start || !end) return "";
  return start.slice(0, 10) === end.slice(0, 10) ? shortDate(start) : `${shortDate(start)} – ${shortDate(end)}`;
}

/**
 * The days a KPI covers, in words. With the CLI's window fields it is the
 * exact range ("Oct 15 – Nov 13"); an older CLI reports only the newest day
 * with data, which is not always the window's last day, so the words say
 * that instead of guessing the range.
 */
export function kpiPeriod(kpi: Kpi | undefined, windowDays = 30): string {
  const days = kpi?.daysInWindow || windowDays;
  if (kpi?.windowStart && kpi.windowEnd) return dateRange(kpi.windowStart, kpi.windowEnd);
  if (kpi?.latestDate) return `last ${days} days · newest data ${shortDate(kpi.latestDate)}`;
  return `last ${days} days`;
}

/**
 * The days each funnel step covers. `shared` is the one range every window
 * total sums (the subtitle names it once); `steps` gives each step its own
 * words: the range when it differs from the shared one, "one reading, <day>"
 * for a step that is a single reading. `undated` is true when no step with a
 * value says which days it covers (an older CLI): the steps may then cover
 * different days and must not be read as a funnel.
 */
export function funnelPeriods(steps: readonly FunnelStep[]): { shared: string; undated: boolean; steps: string[] } {
  const valued = steps.filter((step) => step.value != null);
  const ranges = new Set(valued.filter((step) => step.windowStart && step.windowEnd).map((step) => dateRange(step.windowStart, step.windowEnd)));
  const shared = ranges.size === 1 ? [...ranges][0] : "";
  const undated = valued.length > 0 && ranges.size === 0;
  return {
    shared,
    undated,
    steps: steps.map((step) => {
      if (step.value == null) return "";
      if (step.windowStart && step.windowEnd) {
        const range = dateRange(step.windowStart, step.windowEnd);
        return range === shared ? "" : range;
      }
      return step.asOf ? `one reading, ${shortDate(step.asOf)}` : "one reading";
    }),
  };
}

/**
 * The "why" of one ranking row, never quoting a second 30-day proceeds
 * figure. A CLI that reports the row's window (`window_start`) computes its
 * proceeds over the tile's days, so its reason is shown as written. An older
 * CLI's reason can sum other days than the tile; for an app ranked on its
 * money the sentence is then rebuilt from the tile (the same figure as the
 * app's row and page) with its days.
 */
export function allocationWhy(row: Allocation, app: GrowthApp | undefined): string {
  if (row.windowStart && row.windowEnd) {
    // The CLI writes its 90-day proceeds without dates; they end on the
    // proceeds window's last day (`proceeds90Window`), the days the Proceeds
    // panel sums too. Say them, so both figures carry the same dates.
    const window = proceeds90Window(row.windowEnd);
    if (!window) return row.reason;
    const range = dateRange(window.start, window.end);
    return row.reason.replace(/(\$[\d,]+(?:\.\d+)? (?:proceeds )?in 90 days)/g, `$1, ${range}`);
  }
  const proceeds = app?.kpis.proceeds_usd;
  if ((row.focus === "grow" || row.focus === "fix_data") && proceeds?.value != null) {
    return `${formatValue(proceeds.value, "usd")} proceeds, ${kpiPeriod(proceeds)}. ${FOCUS_META[row.focus]?.meaning ?? ""}`.trim();
  }
  return /\bin 30 days\b/.test(row.reason) ? FOCUS_META[row.focus]?.meaning ?? "" : row.reason;
}

/**
 * How many of an app's next moves are chores most apps share: the same kind
 * of move on the same areas appears for at least half of the apps that have
 * moves. Today the ranking fills the list with these, so the page says so.
 */
export function sharedMoves(app: GrowthApp, apps: readonly GrowthApp[]): { shared: number; shown: number; appCount: number } {
  const key = (m: NextMove) => `${m.kind}|${m.areaIds.join(",")}`;
  const withMoves = apps.filter((a) => a.nextMoves.length > 0);
  const counts = new Map<string, number>();
  for (const a of withMoves) for (const k of new Set(a.nextMoves.slice(0, 5).map(key))) counts.set(k, (counts.get(k) ?? 0) + 1);
  const shown = app.nextMoves.slice(0, 5);
  const shared = shown.filter((m) => (counts.get(key(m)) ?? 0) * 2 >= withMoves.length && withMoves.length > 1).length;
  return { shared, shown: shown.length, appCount: withMoves.length };
}

/**
 * The 90 days the CLI sums an app's proceeds over when it ranks apps ("in 90
 * days"): the 90 days ending on the proceeds window's last day (`window_end`
 * on an allocation row), not on the series' last day, which runs ahead of
 * sales data that arrives days late. Null without an end day.
 */
export function proceeds90Window(windowEnd: string): { start: string; end: string } | null {
  const end = (windowEnd ?? "").slice(0, 10);
  const endMs = Date.parse(`${end}T00:00:00Z`);
  if (!end || Number.isNaN(endMs)) return null;
  return { start: new Date(endMs - 89 * 86_400_000).toISOString().slice(0, 10), end };
}

/** Sum a daily series over the days from `start` to `end`, both included (ISO dates). Missing values add nothing. */
export function sumBetween(points: readonly SeriesPoint[] | undefined, start: string, end: string): number {
  return (points ?? []).reduce((sum, p) => (p.date.slice(0, 10) >= start && p.date.slice(0, 10) <= end ? sum + (p.value ?? 0) : sum), 0);
}

/** The first and last day of a daily series' last `days` points: "Aug 18 – Nov 16". */
export function seriesPeriod(points: readonly SeriesPoint[] | undefined, days?: number): string {
  const slice = days ? (points ?? []).slice(-days) : [...(points ?? [])];
  const dated = slice.filter((p) => p.date);
  return dated.length ? dateRange(dated[0].date, dated[dated.length - 1].date) : "";
}

export function shortDate(iso: string): string {
  if (!iso) return "";
  const d = new Date(`${iso.slice(0, 10)}T00:00:00Z`);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleDateString("en-US", { month: "short", day: "numeric", timeZone: "UTC" });
}
