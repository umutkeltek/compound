import Link from "next/link";

import { EmptyDoctrine, Panel } from "@/components/briefing/primitives";
import { FeedDegraded, plainAge, type FeedProjectionStatus } from "@/components/kit";
import { timestampMs } from "@/lib/counts";
import { ago } from "@/lib/time";
import type { ProofBackedChange } from "@/representation";

const SOURCE = "compound workbench --json --recent 500";

/**
 * The latest finished change: what changed (the summary, two lines), who
 * recorded it and when. The proof behind it (receipt and transition ids, the
 * changed → receipted → transitioned chain, and the trace link) sits one
 * depth down, at Investigate. Receipt and transition establish no later
 * reuse, downstream outcome or measured improvement; the card says so there.
 */
export function LatestMoment({
  moment,
  read,
  agentName = "",
  agentLane = "",
}: {
  moment: ProofBackedChange | null;
  read: { status: FeedProjectionStatus; lastGoodAt: string | null };
  /** the recording agent's name, named the way Live names agents ("" = show the recorded id) */
  agentName?: string;
  /** the full owner/lane id when it says more than the agent ("" = nothing to add) */
  agentLane?: string;
}) {
  if (read.status === "unreachable") {
    return (
      <Panel className="px-5 py-5">
        <FeedDegraded label="latest finished change" source={SOURCE} lastGoodAt={read.lastGoodAt} />
      </Panel>
    );
  }

  if (!moment) {
    return (
      <Panel className="px-5 py-5">
        <div className="text-xs font-bold uppercase tracking-wide text-faint">Latest finished change</div>
        <div className="mt-3">
          <EmptyDoctrine>No proof-backed change has been recorded yet.</EmptyDoctrine>
        </div>
      </Panel>
    );
  }

  const lastGood = read.status === "stale" && read.lastGoodAt ? ago(read.lastGoodAt) : "";
  const at = timestampMs(moment.occurredAt);
  const when = at !== null ? plainAge(Math.max(0, Date.now() - at)) : "";
  const who = agentName || moment.producer;

  return (
    <section className="overflow-hidden rounded-lg border border-hair bg-panel shadow-sm">
      <div className="px-5 pb-4 pt-5">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <span className="text-xs font-bold uppercase tracking-wide text-faint">Latest finished change</span>
          <span className="inline-flex items-center gap-1.5 rounded-pill border border-good-line bg-good-soft px-2.5 py-1 text-2xs font-semibold text-good">
            <span className="h-1.5 w-1.5 rounded-full bg-good" aria-hidden />
            {moment.moveLabel}
          </span>
        </div>

        <p className="mt-3 line-clamp-2 text-sm leading-snug text-ink" title={moment.headline}>
          {moment.headline}
        </p>

        <div className="mt-2 flex flex-wrap items-center justify-between gap-x-3 gap-y-1 text-xs text-muted">
          <span>
            Recorded by <strong className="font-semibold text-ink">{who}</strong>
            {agentName && agentLane ? <span className="mono ml-1.5 text-2xs text-faint">{agentLane}</span> : null}
            {when ? <> · <time dateTime={moment.occurredAt}>{when}</time></> : null}
          </span>
          {lastGood ? <span className="text-faint">last read {lastGood}</span> : null}
        </div>
      </div>

      <div data-depth-min="investigate">
        <div className="border-t border-hair px-5 pb-4 pt-3">
          <p className="text-xs text-muted">Reuse and downstream benefit have not been verified.</p>
          <ol className="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-2xs text-faint" aria-label="Improvement evidence">
            <li aria-current="step" className="font-semibold text-ink">Proof-backed change</li>
            <li>Observed reuse · unknown</li>
            <li>Downstream outcome · unknown</li>
            <li>Measured improvement · unknown</li>
          </ol>
          <div className="mt-4">
            <div
              className="flex h-2 gap-1 rounded-pill bg-soft-2 p-0.5"
              role="img"
              aria-label="Proof chain: changed, receipted, transitioned"
            >
              <span className="flex-1 rounded-pill bg-work" aria-hidden />
              <span className="flex-1 rounded-pill bg-good" aria-hidden />
              <span className="flex-1 rounded-pill bg-route" aria-hidden />
            </div>
            <div className="mt-2 grid grid-cols-3 text-2xs font-medium text-faint" aria-hidden>
              <span>Changed</span>
              <span className="text-center">Receipted</span>
              <span className="text-right">Transitioned</span>
            </div>
          </div>
        </div>

        <div className="grid grid-cols-2 border-t border-hair bg-soft">
          <div className="min-w-0 px-5 py-3.5">
            <div className="text-2xs font-semibold text-muted">Receipt</div>
            <div className="mono mt-1 truncate text-2xs text-faint" title={moment.receiptRef}>
              {moment.receiptRef}
            </div>
          </div>
          <div className="min-w-0 border-l border-hair px-5 py-3.5">
            <div className="flex items-center justify-between gap-2">
              <div className="text-2xs font-semibold text-muted">Transition</div>
              {moment.workId ? (
                <Link
                  href={`/work/${moment.workId}`}
                  className="text-2xs font-semibold text-work hover:underline focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-focus"
                >
                  Open trace →
                </Link>
              ) : null}
            </div>
            <div className="mono mt-1 truncate text-2xs text-faint" title={moment.transitionRef}>
              {moment.transitionRef}
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
