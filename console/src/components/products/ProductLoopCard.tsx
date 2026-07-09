import { CopyCmd } from "@/components/CopyCmd";
import { Chip } from "@/components/ui";
import { ago } from "@/lib/time";
import {
  formatMetricValue,
  groupSignalsByLens,
  metricLabel,
  metricSurface,
  nextMove,
  productDisplayName,
  verdictTone,
  type ProductLoop,
  type ProductSignal,
} from "@/lib/products";

/*
 * One product = one identity, rendered as its loop: signals (evidence) →
 * hypothesis → review → verdict, with freshness and the next move. This is the
 * "product as convergence point" the whole design turns on.
 */

function FreshPill({ stale, latestSignalAt }: { stale: boolean; latestSignalAt: string }) {
  // A loop that never had a signal isn't "stale" — it hasn't started. Only a
  // once-fed loop that went quiet is stale.
  const hasSignal = Boolean(latestSignalAt);
  const state = !hasSignal ? "idle" : stale ? "stale" : "fresh";
  const palette = {
    idle: { bg: "#f1f5f9", fg: "#64748b", dot: "#9aa4b2" },
    stale: { bg: "#fef3c7", fg: "#b45309", dot: "#f59e0b" },
    fresh: { bg: "#ecfdf5", fg: "#047857", dot: "#10b981" },
  }[state];
  const text = hasSignal ? `${state} · last signal ${ago(latestSignalAt)}` : "no signal yet";
  return (
    <span
      className="inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-[11px] font-medium"
      style={{ background: palette.bg, color: palette.fg }}
    >
      <span className="h-1.5 w-1.5 rounded-full" style={{ background: palette.dot }} />
      {text}
    </span>
  );
}

function Stage({ label, value, dim }: { label: string; value: string; dim?: boolean }) {
  return (
    <div
      className={`flex flex-col items-center rounded-lg border px-3 py-1.5 ${
        dim ? "border-dashed border-line bg-canvas/40 text-ink-3" : "border-line bg-white"
      }`}
    >
      <span className="text-[15px] font-semibold leading-none">{value}</span>
      <span className="mt-0.5 text-[10px] uppercase tracking-wide text-ink-3">{label}</span>
    </div>
  );
}

function Arrow() {
  return <span className="text-ink-3">▸</span>;
}

function MetricRow({ s }: { s: ProductSignal }) {
  const surface = metricSurface(s.metric, s.provider);
  const before = formatMetricValue(s.metric, s.before);
  const after = formatMetricValue(s.metric, s.after);
  const changed = s.before != null && s.after != null && s.before !== s.after;
  const up = changed && (s.after as number) > (s.before as number);
  return (
    <div className="flex items-center justify-between gap-3 py-1">
      <div className="flex min-w-0 items-center gap-2">
        <Chip tone={surface.accent}>{surface.label}</Chip>
        <span className="truncate text-[12.5px] text-ink">{metricLabel(s.metric)}</span>
      </div>
      <div className="flex shrink-0 items-center gap-1.5 text-[12.5px]">
        {changed ? (
          <>
            <span className="text-ink-3">{before}</span>
            <span className={up ? "text-emerald-600" : "text-red-500"}>→ {after}</span>
          </>
        ) : (
          <span className="font-medium text-ink" title="before == after (snapshot)">
            {after}
          </span>
        )}
      </div>
    </div>
  );
}

export function ProductLoopCard({ loop }: { loop: ProductLoop }) {
  const move = nextMove(loop);
  const latestReview = loop.reviews[0];
  const empty = loop.signalCount === 0;

  return (
    <div className="card flex flex-col gap-4 p-5">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h2 className="text-[16px] font-semibold text-ink">{productDisplayName(loop.productId)}</h2>
          <p className="mt-0.5 text-[11.5px] text-ink-3">
            {empty ? "connected · awaiting first signal" : `${loop.metricNames.length} metrics tracked`}
          </p>
        </div>
        <FreshPill stale={loop.freshness.stale} latestSignalAt={loop.freshness.latestSignalAt} />
      </div>

      {/* the loop as a flow */}
      <div className="flex flex-wrap items-center gap-2">
        <Stage label="signals" value={String(loop.signalCount)} dim={loop.signalCount === 0} />
        <Arrow />
        <Stage label="hypotheses" value={String(loop.hypothesisCount)} dim={loop.hypothesisCount === 0} />
        <Arrow />
        <Stage label="reviews" value={String(loop.reviews.length)} dim={loop.reviews.length === 0} />
        {latestReview ? (
          <>
            <Arrow />
            <Chip tone={verdictTone(latestReview.verdict)}>{latestReview.verdict}</Chip>
          </>
        ) : null}
      </div>

      {/* signals through domain lenses (a view of the same evidence) */}
      {!empty ? (
        <div className="rounded-lg border border-line bg-canvas/30 px-3 py-2.5">
          <div className="section-label mb-2">Lenses · the same signals, read by what matters</div>
          <div className="flex flex-col gap-3">
            {groupSignalsByLens(loop.signals).map(({ lens, meta, signals }) => (
              <div key={lens}>
                <div className="mb-0.5 flex items-center gap-2">
                  <Chip tone={meta.accent}>{meta.label}</Chip>
                  <span className="text-[10.5px] text-ink-3">{meta.question}</span>
                </div>
                {signals.map((s) => (
                  <MetricRow key={s.signalId} s={s} />
                ))}
              </div>
            ))}
          </div>
        </div>
      ) : null}

      {/* the verdict's own next-action, if a review exists */}
      {latestReview ? (
        <p className="text-[11.5px] text-ink-3">
          Reviewed {ago(latestReview.reviewedAt)} → <span className="font-medium">{latestReview.verdict}</span>. Causality
          stays unproven until a before/after change event is reviewed.
        </p>
      ) : null}

      {/* next move (the doctor's answer) */}
      <div className="rounded-lg border border-line bg-white px-3 py-2.5">
        <div className="section-label mb-1.5">Next move</div>
        <p className="mb-2 text-[12.5px] text-ink">{move.text}</p>
        <CopyCmd cmd={move.cmd} />
      </div>
    </div>
  );
}
