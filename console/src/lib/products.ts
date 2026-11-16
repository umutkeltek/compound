import { HEAVY_PROJECTION_TIMEOUT_MS, arr, num, readProjection, runJson, str, type ReadState } from "@/lib/compound";
import { humanizeProductId } from "@/lib/app-identity";
import type { Accent } from "@/components/ui";

/*
 * Product loop board — the product as convergence point. A product is one
 * identity; this joins its signals (evidence), hypotheses, reviews (verdicts),
 * freshness, and gaps into one loop. Reads `compound product loops --json` +
 * `compound product authority --json` (read-only projections).
 */

export type ProductSignal = {
  signalId: string;
  provider: string;
  metric: string;
  before: number | null;
  after: number | null;
  observedAt: string;
  changeEvent: string;
  transitionRef: string;
};

export type ProductReview = {
  id: string;
  hypothesisId: string;
  signalId: string;
  verdict: string;
  reviewedAt: string;
};

/** An open idea about a product: what was changed, what it should do, and over which days it is watched. */
export type ProductHypothesis = {
  id: string;
  statement: string;
  expectedEffect: string;
  metrics: string[];
  /** "2030-01-01..2030-01-28" as recorded */
  window: string;
  /** a machine draft whose expected effect nobody wrote: nothing to judge */
  placeholder: boolean;
};

export type ProductLoop = {
  productId: string;
  name: string;
  /** other ids the CLI folded into this row (newer CLIs); the page also folds through the app catalog */
  aliases: string[];
  hypotheses: ProductHypothesis[];
  signals: ProductSignal[];
  metricNames: string[];
  hypothesisCount: number;
  reviews: ProductReview[];
  sourceIssueCount: number;
  freshness: { latestSignalAt: string; latestReviewAt: string; stale: boolean };
  loopGaps: string[];
  signalCount: number;
};

export type ProductPortfolioRow = {
  productId: string;
  status: string;
  signalCount: number;
  hypothesisCount: number;
  pendingReviewCount: number;
  reviewReadyCount: number;
  sourceIssueCount: number;
};

function numOrNull(v: unknown): number | null {
  return typeof v === "number" ? v : null;
}

function mapSignal(s: Record<string, unknown>): ProductSignal {
  return {
    signalId: str(s.signal_id),
    provider: str(s.provider),
    metric: str(s.metric),
    before: numOrNull(s.before),
    after: numOrNull(s.after),
    observedAt: str(s.observed_at),
    changeEvent: str(s.change_event),
    transitionRef: str(s.transition_ref),
  };
}

/**
 * A draft hypothesis made from a data snapshot carries a placeholder expected
 * effect that a person was meant to write. Approving refuses it, so it is
 * never a verdict that waits for anyone.
 */
export function isPlaceholderHypothesis(h: { expectedEffect: string }): boolean {
  const effect = h.expectedEffect.trim();
  return !effect || /^OPERATOR MUST WRITE\b/i.test(effect);
}

function mapHypothesis(raw: unknown): ProductHypothesis {
  const o = (raw ?? {}) as Record<string, unknown>;
  const h = {
    id: str(o.id),
    statement: str(o.statement),
    expectedEffect: str(o.expected_effect),
    metrics: arr(o.metrics).map(str).filter(Boolean),
    window: str(o.observation_window),
  };
  return { ...h, placeholder: isPlaceholderHypothesis(h) };
}

function mapLoop(p: Record<string, unknown>): ProductLoop {
  const byMetric = (p.signals_by_metric ?? {}) as Record<string, unknown>;
  const signals: ProductSignal[] = [];
  const metricNames: string[] = [];
  for (const [metric, rows] of Object.entries(byMetric)) {
    metricNames.push(metric);
    for (const r of arr(rows)) signals.push(mapSignal(r as Record<string, unknown>));
  }
  const fresh = (p.freshness ?? {}) as Record<string, unknown>;
  return {
    productId: str(p.product_id),
    name: str(p.name) || str(p.product_id),
    aliases: arr(p.aliases).map(str).filter(Boolean),
    hypotheses: arr(p.open_hypotheses).map(mapHypothesis),
    signals,
    metricNames: metricNames.sort(),
    hypothesisCount: arr(p.open_hypotheses).length,
    reviews: arr(p.reviews).map((r) => {
      const o = r as Record<string, unknown>;
      return {
        id: str(o.id),
        hypothesisId: str(o.hypothesis_id),
        signalId: str(o.signal_id),
        verdict: str(o.verdict),
        reviewedAt: str(o.reviewed_at),
      };
    }),
    sourceIssueCount:
      num((p.open_source_issues as Record<string, unknown>)?.count) || 0,
    freshness: {
      latestSignalAt: str(fresh.latest_signal_at),
      latestReviewAt: str(fresh.latest_review_at),
      stale: fresh.stale === true,
    },
    loopGaps: arr(p.loop_gaps).map(str).filter(Boolean),
    signalCount: signals.length,
  };
}

export type ProductLoopsReport = {
  generatedAt: string;
  loops: ProductLoop[];
  status: ReadState;
  /** the first read is still queued or running; `loops` is not an answer yet */
  checking: boolean;
  error: string | null;
};

/** Loops plus the projection's own generated_at, so the page can stamp a
 *  FreshnessChip against the real read time (not a fabricated one).
 *  `product loops` reads the release-observation inventory and takes ~25s on
 *  a real home, past the default 15s budget; a timed-out read used to render
 *  as "No loops yet". The read is non-blocking: the page paints from the
 *  cached copy and says it is checking when there is none. */
export async function getProductLoopsReport(): Promise<ProductLoopsReport> {
  const read = await readProjection(["product", "loops", "--json"], { timeoutMs: HEAVY_PROJECTION_TIMEOUT_MS });
  const j = read.value;
  return {
    generatedAt: str(j?.generated_at),
    loops: arr(j?.products)
      .map((p) => mapLoop(p as Record<string, unknown>))
      .sort((a, b) => b.signalCount - a.signalCount),
    status: read.status,
    checking: read.status === "loading" || read.updating,
    error: read.error,
  };
}

export async function getProductLoops(): Promise<ProductLoop[]> {
  return (await getProductLoopsReport()).loops;
}

export async function getProductPortfolio(): Promise<ProductPortfolioRow[]> {
  const j = await runJson(["product", "authority", "--json"]);
  return arr(j?.products).map((p) => {
    const o = p as Record<string, unknown>;
    return {
      productId: str(o.product_id),
      status: str(o.status),
      signalCount: num(o.signal_count),
      hypothesisCount: num(o.hypothesis_count),
      pendingReviewCount: num(o.pending_review_count),
      reviewReadyCount: num(o.review_ready_count),
      sourceIssueCount: num(o.source_issue_count),
    };
  });
}

/** A card compares the latest observation for each provider/metric pair.
 * The complete history remains in the owned product projection. */
export function latestProductSignals(signals: ProductSignal[]): ProductSignal[] {
  const latest = new Map<string, ProductSignal>();
  const time = (value: string) => Number.isFinite(Date.parse(value)) ? Date.parse(value) : -Infinity;
  for (const signal of signals) {
    const key = JSON.stringify([signal.provider, signal.metric]);
    const previous = latest.get(key);
    if (!previous || time(signal.observedAt) > time(previous.observedAt)) latest.set(key, signal);
  }
  return [...latest.values()];
}

/* ---------- one card per app ---------- */

/** An app from the catalog (`compound product growth --json` apps): its id, name and other ids. */
export type LoopCatalogApp = { readonly productId: string; readonly name: string; readonly aliases?: readonly string[] };

/** Lowercase letters and digits of an id, `product:` dropped: "product:Pocket_Log" → "pocketlog". */
function loopKey(id: string): string {
  return id.trim().toLowerCase().replace(/^product:/, "").replace(/[^\p{L}\p{N}]+/gu, "");
}

/**
 * One card per app. Rows whose id or aliases name the same catalog app (by
 * any spelling) fold into one, named and keyed by the catalog. Rows no
 * catalog app claims stay as they are. `missing` lists catalog apps with no
 * row at all, so the page can say so instead of leaving them out silently.
 */
export function foldLoopsByApp(loops: readonly ProductLoop[], catalog: readonly LoopCatalogApp[] | null): { loops: ProductLoop[]; missing: LoopCatalogApp[] } {
  const byKey = new Map<string, LoopCatalogApp>();
  for (const app of catalog ?? []) for (const id of [app.productId, app.name, ...(app.aliases ?? [])]) if (loopKey(id) && !byKey.has(loopKey(id))) byKey.set(loopKey(id), app);
  const groups = new Map<string, { app: LoopCatalogApp | null; rows: ProductLoop[] }>();
  for (const loop of loops) {
    const app = [loop.productId, ...loop.aliases].map((id) => byKey.get(loopKey(id))).find(Boolean) ?? null;
    const key = app ? app.productId : loopKey(loop.productId) || loop.productId;
    const group = groups.get(key) ?? { app, rows: [] };
    group.rows.push(loop);
    groups.set(key, group);
  }
  const folded = [...groups.values()].map(({ app, rows }) => {
    const signals = rows.flatMap((r) => r.signals);
    const reviews = rows.flatMap((r) => r.reviews).sort((a, b) => b.reviewedAt.localeCompare(a.reviewedAt));
    const latest = (pick: (r: ProductLoop) => string) => rows.map(pick).filter(Boolean).sort().at(-1) ?? "";
    const ids = [...new Set(rows.flatMap((r) => [r.productId, ...r.aliases]))];
    const productId = app?.productId ?? rows[0].productId;
    return {
      productId,
      name: app?.name ?? (rows[0].name === rows[0].productId ? humanizeProductId(rows[0].productId) : rows[0].name),
      aliases: ids.filter((id) => id !== productId),
      hypotheses: rows.flatMap((r) => r.hypotheses),
      signals,
      metricNames: [...new Set(rows.flatMap((r) => r.metricNames))].sort(),
      hypothesisCount: rows.reduce((n, r) => n + r.hypothesisCount, 0),
      reviews,
      sourceIssueCount: rows.reduce((n, r) => n + r.sourceIssueCount, 0),
      freshness: {
        latestSignalAt: latest((r) => r.freshness.latestSignalAt),
        latestReviewAt: latest((r) => r.freshness.latestReviewAt),
        stale: rows.filter((r) => r.signalCount > 0).every((r) => r.freshness.stale),
      },
      loopGaps: [...new Set(rows.flatMap((r) => r.loopGaps))],
      signalCount: signals.length,
    } satisfies ProductLoop;
  });
  const present = new Set(folded.map((l) => l.productId));
  return { loops: folded, missing: (catalog ?? []).filter((app) => !present.has(app.productId)) };
}

/** A real verdict waits: the CLI says one is ready and at least one open idea has an expected effect someone wrote. */
export function verdictDue(loop: ProductLoop): boolean {
  return loop.loopGaps.includes("review_ready") && loop.hypotheses.some((h) => !h.placeholder);
}

/** The Decide address of an app's product results decision. */
export function productResultsDecision(productId: string): string {
  return `product-reviews|${productId}`;
}

/* ---------- display helpers (plain language, never system slugs) ---------- */

export function productDisplayName(productId: string): string {
  return productId.replace(/^product:/, "");
}

const METRIC_LABEL: Record<string, string> = {
  "app.conversion_rate": "Conversion rate",
  "app.downloads_first_time": "First-time downloads",
  "app.impressions": "Impressions",
  app_store_connect_review_count: "App Store reviews",
  app_store_connect_average_rating: "Average rating",
  gsc_clicks: "Search clicks",
  gsc_impressions: "Search impressions",
  gsc_ctr: "Search CTR",
  gsc_avg_position: "Search position",
  ga4_active_users: "Active users",
  ga4_sessions: "Sessions",
  ga4_conversions: "Conversions",
  revenuecat_mrr: "MRR",
  revenuecat_active_subscriptions: "Active subscriptions",
  ads_spend: "Ad spend",
  ads_impressions: "Ad impressions",
  ads_clicks: "Ad clicks",
  ads_conversions: "Ad conversions",
  ads_revenue: "Ad revenue",
  ads_ctr: "Ad CTR",
  ads_cpc: "Cost per click",
  ads_cpa: "Cost per conversion",
  ads_roas: "ROAS",
};

const AD_PLATFORM_LABEL: Record<string, string> = {
  apple_search_ads: "Apple Search Ads",
  google_ads: "Google Ads",
  meta_ads: "Meta Ads",
  tiktok_ads: "TikTok Ads",
  x_ads: "X Ads",
  reddit_ads: "Reddit Ads",
  linkedin_ads: "LinkedIn Ads",
  other: "Ads",
};

/**
 * A metric in words. Known metrics have a name; others drop the source prefix
 * (the card already shows the source) and say "US dollars" for `_usd`.
 */
export function metricLabel(metric: string): string {
  if (METRIC_LABEL[metric]) return METRIC_LABEL[metric];
  const bare = metric
    .replace(/^(app_store_connect|app_store|app|gsc|ga4|ads|posthog|revenuecat)[._]/, "")
    .replace(/_usd$/, " in US dollars")
    .replace(/[._]+/g, " ")
    .trim();
  return bare ? bare.charAt(0).toUpperCase() + bare.slice(1) : metric;
}

export function metricSurface(metric: string, provider: string): { label: string; accent: Accent } {
  if (provider === "app_store_connect" || metric.startsWith("app.") || metric.startsWith("app_store"))
    return { label: "App Store", accent: "blue" };
  if (provider === "google_search_console" || metric.startsWith("gsc_"))
    return { label: "Search Console", accent: "green" };
  if (provider === "google_analytics" || metric.startsWith("ga4_"))
    return { label: "Analytics", accent: "orange" };
  if (provider === "revenuecat" || metric.startsWith("revenuecat"))
    return { label: "RevenueCat", accent: "violet" };
  if (provider === "posthog" || metric.startsWith("posthog"))
    return { label: "PostHog", accent: "pink" };
  if (AD_PLATFORM_LABEL[provider] || metric.startsWith("ads_"))
    return { label: AD_PLATFORM_LABEL[provider] ?? "Ads", accent: "red" };
  return { label: "Canonical", accent: "gray" };
}

export function formatMetricValue(metric: string, value: number | null): string {
  if (value == null) return "—";
  if (metric === "ads_conversions") return Number.isInteger(value) ? value.toLocaleString() : value.toFixed(1);
  if (metric === "ads_roas") return `${value.toFixed(2)}×`;
  if (metric.includes("conversion") || metric.includes("ctr"))
    return `${(value * 100).toFixed(2)}%`;
  if (metric.includes("position")) return value.toFixed(1);
  if (Number.isInteger(value)) return value.toLocaleString();
  return value.toFixed(2);
}

export function verdictTone(verdict: string): Accent {
  switch (verdict) {
    case "supported":
      return "green";
    case "refuted":
      return "red";
    case "inconclusive":
      return "orange";
    default:
      return "gray";
  }
}

/* ---------- domain lenses (a VIEW of the same signals, not new claims) ----------
 * The loop adapts to its domain: a paid-app loop populates monetization/reach/
 * quality; a search loop populates reach/quality; a subscription loop populates
 * monetization. A lens only appears if the loop has a metric that feeds it, so
 * the perspectives are domain-driven, never silently generic. Anything that maps
 * to no lens surfaces under "Unlensed" — shown, not hidden. */

export type LoopLens = "monetization" | "reach" | "quality" | "engagement" | "other";

export const LENS_META: Record<LoopLens, { label: string; question: string; accent: Accent }> = {
  monetization: { label: "Monetization", question: "converting & earning?", accent: "green" },
  reach: { label: "Reach", question: "are people finding it?", accent: "blue" },
  quality: { label: "Quality", question: "well-received & ranking?", accent: "violet" },
  engagement: { label: "Engagement", question: "do they come back?", accent: "orange" },
  other: { label: "Unlensed", question: "no domain lens matched yet", accent: "gray" },
};

const LENS_ORDER: LoopLens[] = ["monetization", "reach", "quality", "engagement", "other"];

export function lensForMetric(metric: string): LoopLens {
  const m = metric.toLowerCase();
  if (/^ads_(spend|cpc|cpa|cpm)$/.test(m)) return "reach";
  if (/(conversion|proceeds|revenue|mrr|subscription|earnings|purchase|arpu|ltv|rpm|roas)/.test(m)) return "monetization";
  if (/(activation|retention|\bdau\b|\bmau\b|active_users|returning|session)/.test(m)) return "engagement";
  if (/(rating|review_count|position|ctr|rank|crash|stars)/.test(m)) return "quality";
  if (/(impression|click|download|install|first_time|page_view|reach|new_users|visits)/.test(m)) return "reach";
  return "other";
}

export function groupSignalsByLens(
  signals: ProductSignal[],
): { lens: LoopLens; meta: (typeof LENS_META)[LoopLens]; signals: ProductSignal[] }[] {
  const buckets = new Map<LoopLens, ProductSignal[]>();
  for (const s of signals) {
    const lens = lensForMetric(s.metric);
    (buckets.get(lens) ?? buckets.set(lens, []).get(lens)!).push(s);
  }
  return LENS_ORDER.filter((l) => buckets.has(l)).map((lens) => ({
    lens,
    meta: LENS_META[lens],
    signals: (buckets.get(lens) ?? []).sort((a, b) => metricLabel(a.metric).localeCompare(metricLabel(b.metric))),
  }));
}

/**
 * What happens next for one app's results, in one plain sentence: who acts,
 * or that nothing waits. No command: commands live behind a disclosure for
 * agents, and never with a placeholder.
 */
export function nextMove(loop: ProductLoop): { text: string; cmd: string } {
  const doctor = `compound product doctor ${loop.productId}`;
  if (loop.signalCount === 0) {
    return { text: "Nothing measured yet. Results start once a data source reports for this app.", cmd: "" };
  }
  if (verdictDue(loop)) {
    return { text: "A change was measured against what it was expected to do. Your verdict is due.", cmd: doctor };
  }
  if (loop.loopGaps.includes("review_ready")) {
    return {
      text: "Nothing waits for you. The only open ideas are drafts made from a data snapshot, with no expected effect written, so there is no change to judge.",
      cmd: doctor,
    };
  }
  if (loop.loopGaps.includes("signals_without_hypothesis")) {
    return { text: `${loop.signalCount} observations, but no change with an expected effect is recorded, so there is nothing to judge yet.`, cmd: doctor };
  }
  if (loop.loopGaps.includes("hypothesis_without_signal")) {
    return { text: "An idea waits for its first matching measurement.", cmd: doctor };
  }
  return { text: "Up to date: nothing open.", cmd: doctor };
}
