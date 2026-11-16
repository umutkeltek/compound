import Link from "next/link";

import { humanizeProductId } from "@/lib/app-identity";
import { appHref, decideHref } from "@/lib/links";
import { CopyCmd } from "@/components/CopyCmd";
import { IdRef, StateBadge } from "@/components/kit";
import { ago } from "@/lib/time";
import {
  formatMetricValue,
  groupSignalsByLens,
  latestProductSignals,
  metricLabel,
  metricSurface,
  nextMove,
  productResultsDecision,
  verdictDue,
  type ProductLoop,
  type ProductSignal,
} from "@/lib/products";

/*
 * One product = one identity, rendered as its loop: signals (evidence) →
 * hypothesis → review → verdict, with freshness and the next move. Restyled on
 * the kit (DESIGN.md): card grammar, the 7-tone StateBadge, a done→now→next
 * hypothesis chain (the cockpit pattern), token-only color. A loop that is
 * "blocked on you" (a signal+hypothesis matched, verdict due) carries the amber
 * admission seam and floats to the top of the page.
 */

/** Map a loop onto its lifecycle state + a plain label for the StateBadge. */
export function loopState(loop: ProductLoop): { state: string; label: string; blockedOnYou: boolean } {
  if (verdictDue(loop)) return { state: "needs_you", label: "verdict due", blockedOnYou: true };
  if (loop.signalCount === 0) return { state: "candidate", label: "awaiting signal", blockedOnYou: false };
  const verdict = loop.reviews[0]?.verdict;
  if (verdict === "supported") return { state: "completed", label: "supported", blockedOnYou: false };
  if (verdict === "refuted") return { state: "failed", label: "refuted", blockedOnYou: false };
  if (verdict === "inconclusive") return { state: "held", label: "inconclusive", blockedOnYou: false };
  if (loop.hypothesisCount > 0) return { state: "running", label: "testing", blockedOnYou: false };
  return { state: "watching", label: "collecting", blockedOnYou: false };
}

/** Last-signal freshness — respects the projection's server-computed `stale`
 *  flag (not a client TTL), so it never disagrees with the backend. */
function SignalFreshness({ stale, latestSignalAt }: { stale: boolean; latestSignalAt: string }) {
  const hasSignal = Boolean(latestSignalAt);
  const tone = !hasSignal ? "text-faint" : stale ? "text-seam" : "text-good";
  const dot = !hasSignal ? "bg-faint" : stale ? "bg-seam" : "bg-good";
  const text = hasSignal ? `${stale ? "stale" : "live"} · last signal ${ago(latestSignalAt)}` : "no signal yet";
  return (
    <span
      className={`mono inline-flex items-center gap-1.5 rounded-pill border border-hair bg-soft px-2.5 py-0.5 text-2xs font-semibold tabular-nums ${tone}`}
    >
      <span className={`h-1.5 w-1.5 rounded-full ${dot}`} aria-hidden />
      {text}
    </span>
  );
}

function SurfaceChip({ label }: { label: string }) {
  return (
    <span className="inline-flex items-center rounded-pill border border-hair bg-soft px-2 py-0.5 text-2xs font-semibold text-muted">
      {label}
    </span>
  );
}

function observedDay(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "" : d.toLocaleDateString("en-US", { month: "short", day: "numeric", year: "numeric", timeZone: "UTC" });
}

function MetricRow({ s }: { s: ProductSignal }) {
  const surface = metricSurface(s.metric, s.provider);
  const before = formatMetricValue(s.metric, s.before);
  const after = formatMetricValue(s.metric, s.after);
  const changed = s.before != null && s.after != null && s.before !== s.after;
  return (
    <div className="flex items-center justify-between gap-3 py-1">
      <div className="flex min-w-0 items-center gap-2">
        <SurfaceChip label={surface.label} />
        <span className="truncate text-xs text-ink">{metricLabel(s.metric)}</span>
      </div>
      <div className="flex shrink-0 items-center gap-1.5 text-xs tabular-nums">
        {changed ? (
          <>
            <span className="text-faint">{before}</span>
            <span className="text-muted">→ {after}</span>
          </>
        ) : (
          <span className="font-medium text-ink" title="one reading, not a before and after">
            {after}
          </span>
        )}
        {observedDay(s.observedAt) ? <span className="text-2xs text-faint">· {observedDay(s.observedAt)}</span> : null}
      </div>
    </div>
  );
}

export function ProductLoopCard({ loop }: { loop: ProductLoop }) {
  const move = nextMove(loop);
  const latestReview = loop.reviews[0];
  const empty = loop.signalCount === 0;
  const { state, label, blockedOnYou } = loopState(loop);
  const transitionRef = loop.signals.find((s) => s.transitionRef)?.transitionRef ?? "";
  const realIdeas = loop.hypotheses.filter((h) => !h.placeholder);

  return (
    <article
      className={`flex flex-col gap-4 rounded-lg border border-hair bg-panel p-4 shadow-sm ${
        blockedOnYou ? "admission-seam" : ""
      }`}
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 className="text-md font-semibold text-ink"><Link href={appHref(loop.productId)} className="hover:underline">{loop.name || humanizeProductId(loop.productId)}</Link></h2>
          <p className="mt-0.5 text-2xs text-faint">
            {empty
              ? "connected · awaiting first signal"
              : `${loop.metricNames.length} metric${loop.metricNames.length === 1 ? "" : "s"} tracked`}
          </p>
        </div>
        <div className="flex shrink-0 flex-col items-end gap-1.5">
          <StateBadge state={state} label={label} />
          <SignalFreshness stale={loop.freshness.stale} latestSignalAt={loop.freshness.latestSignalAt} />
        </div>
      </div>

      {/* Counts summarize the full history; metric disclosure shows latest observations. */}
      <div className="flex flex-wrap gap-3 text-xs text-muted">
        <span>{loop.signalCount} observations</span>
        <span>{realIdeas.length} {realIdeas.length === 1 ? "idea under test" : "ideas under test"}</span>
        <span>{loop.reviews.length} {loop.reviews.length === 1 ? "verdict" : "verdicts"} recorded</span>
      </div>

      {realIdeas.length ? (
        <ul className="flex flex-col gap-2">
          {realIdeas.map((h) => (
            <li key={h.id} className="rounded-md border border-hair bg-soft px-3 py-2 text-xs">
              <div className="font-medium text-ink">{h.statement || "A change"}</div>
              <div className="mt-0.5 text-muted">Expected: {h.expectedEffect}</div>
              {h.window ? <div className="mt-0.5 text-faint">Watched {h.window.replace("..", " to ")}</div> : null}
            </li>
          ))}
        </ul>
      ) : null}

      {/* signals through domain lenses (a view of the same evidence) */}
      {!empty ? (
        <details className="rounded-md border border-hair bg-soft px-3 py-2.5">
          <summary className="cursor-pointer text-xs font-semibold text-muted">Latest reading of each metric, with its date</summary>
          <div className="flex flex-col gap-3">
            {groupSignalsByLens(latestProductSignals(loop.signals)).map(({ lens, meta, signals }) => (
              <div key={lens}>
                <div className="mb-0.5 flex items-center gap-2">
                  <SurfaceChip label={meta.label} />
                  <span className="text-2xs text-faint">{meta.question}</span>
                </div>
                {signals.map((s) => (
                  <MetricRow key={s.signalId} s={s} />
                ))}
              </div>
            ))}
          </div>
        </details>
      ) : null}

      {/* the verdict's own next-action, if a review exists */}
      {latestReview ? (
        <p className="text-2xs text-faint">
          Last verdict {ago(latestReview.reviewedAt)}:{" "}
          <span className="font-medium text-muted">{latestReview.verdict}</span>. A verdict is a judgement, not proof that
          the change caused the result.
        </p>
      ) : null}

      {/* next move (the doctor's answer) */}
      <div className="rounded-md border border-hair bg-soft px-3 py-2.5">
        <div className="section-label mb-1.5">What happens next</div>
        <p className="text-xs text-ink">{move.text}</p>
        {blockedOnYou ? (
          <Link
            href={decideHref({ decision: productResultsDecision(loop.productId) })}
            className="mt-2 inline-flex items-center gap-1 text-xs font-medium text-work underline-offset-2 hover:underline"
          >
            Give the verdict →
          </Link>
        ) : null}
        {move.cmd ? (
          <details className="mt-2 text-xs text-muted">
            <summary className="cursor-pointer text-2xs">For agents</summary>
            <CopyCmd cmd={move.cmd} />
          </details>
        ) : null}
      </div>

      {transitionRef ? (
        <div className="flex items-center gap-1.5 text-2xs text-faint">
          <span>Linked to a recorded change</span>
          <IdRef id={transitionRef} kind="transition" />
        </div>
      ) : null}
    </article>
  );
}
