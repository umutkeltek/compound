import Link from "next/link";

import { IdRef } from "@/components/kit";
import { memorySubject } from "@/components/memory/presentation";
import { appHref, memoryCandidateHref } from "@/lib/links";
import { standingLawReadinessChip, type MemoryCandidate } from "@/lib/memory-data";
import { KNOWLEDGE_TERMS, sentence } from "@/lib/plain-words";
import { ago } from "@/lib/time";

/*
 * A proposed rule, shown on Rules next to the rules it would join. Read-only
 * here: the choice is made on the Memory page, and the link lands on this
 * proposal's own card there (`/memory#<id>`), not at the top of the page.
 *
 * The "seen N×" chip (`standingLawReadinessChip`) is a cue derived from how
 * often the same statement was recorded; it decides nothing.
 */

const CHIP_TONE: Record<"seam" | "good" | "muted", string> = {
  seam: "border-seam-line bg-seam-soft text-seam",
  good: "border-good-line bg-good-soft text-good",
  muted: "border-hair bg-soft-2 text-muted",
};

function headLine(candidate: MemoryCandidate): string {
  const statement = String(candidate.statement ?? candidate.object ?? "").trim();
  return statement || "The proposal has no recorded wording. Open it on the Memory page.";
}

export function StandingLawCandidateCard({ candidate }: { candidate: MemoryCandidate }) {
  const headline = headLine(candidate);
  const about = memorySubject(String(candidate.entity ?? ""));
  const chip = standingLawReadinessChip(candidate);
  const createdAt = String(candidate.created_at ?? "");
  const age = createdAt ? ago(createdAt) : "";

  return (
    <article className="admission-seam flex flex-col gap-2 rounded-lg border border-hair bg-panel px-4 py-3 shadow-sm">
      <div className="flex items-start justify-between gap-2">
        <div className="flex min-w-0 flex-col gap-0.5">
          <span className="text-2xs font-bold uppercase tracking-wide text-faint">{sentence(KNOWLEDGE_TERMS.proposedRule.name)}</span>
          <h3 className="text-md font-semibold leading-snug text-ink">{headline}</h3>
          {about ? (
            <div className="text-xs text-muted">
              About:{" "}
              {about.app ? (
                <Link href={appHref(about.app)} title={about.raw} className="font-semibold text-work hover:underline">{about.label}</Link>
              ) : (
                <span className="font-semibold text-ink" title={about.raw}>{about.label}</span>
              )}
            </div>
          ) : null}
        </div>
        <div className="flex shrink-0 flex-col items-end gap-1">
          <span className="rounded-pill border border-seam-line bg-seam-soft px-2.5 py-0.5 text-xs font-semibold text-seam">Waiting for you</span>
          {chip ? (
            <span
              title="How often this statement was recorded, and whether that is rising. A cue only; it decides nothing."
              className={`inline-flex items-center rounded-pill border px-2 py-0.5 text-2xs font-semibold tabular-nums ${CHIP_TONE[chip.tone]}`}
            >
              {chip.label}
            </span>
          ) : null}
        </div>
      </div>

      <div className="flex flex-wrap items-center justify-between gap-2 border-t border-hair pt-2">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
          <Link
            href={memoryCandidateHref(candidate.id)}
            className="inline-flex items-center gap-1.5 font-semibold text-seam hover:underline focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-focus"
          >
            Decide on the Memory page
            <span aria-hidden>→</span>
          </Link>
          {age ? <span className="tabular-nums text-faint">proposed {age}</span> : null}
        </div>
        <IdRef id={candidate.id} kind="candidate" />
      </div>
    </article>
  );
}
