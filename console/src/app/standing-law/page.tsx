import Link from "next/link";
import { PendingRefresh } from "@/components/PendingRefresh";

import { StandingLawBrowser } from "@/components/standing-law/StandingLawBrowser";
import { StandingLawCandidateCard } from "@/components/standing-law/StandingLawCandidateCard";
import { EmptyState, GavelIcon, hasCopy, PageHeader, SectionHeader, ReadChip, ReadFailedBanner, ReadNotice, readsPending } from "@/components/kit";
import { readProjection, type Json } from "@/lib/compound";
import { unit } from "@/lib/counts";
import { memoryReviewHref } from "@/lib/links";
import { KNOWLEDGE_TERMS } from "@/lib/plain-words";
import { readStandingLawCandidates } from "@/lib/memory-data";
import { buildStandingLaw, type StandingLawConflict } from "@/lib/standing-law";

export const dynamic = "force-dynamic";

/*
 * Standing law — the operator-facing twin of what orient already serves agents.
 * "Did I already rule this?" used to mean grepping worklogs, so settled law got
 * re-litigated every session. This read-only view lists every ACCEPTED
 * ruling/grant/freeze, newest-first and searchable, with verbatim provenance +
 * the supersession chain on disclosure. Contradictions (parked harvest
 * candidates that reverse accepted law) render as a loud strip at the top.
 *
 * Read-only: no ratify/record/adjudicate here. Ratification stays the
 * operator-provenance path; this view never accepts anything.
 */

async function loadStandingLaw() {
  // About 9 s on a loaded home; a 15 s budget killed it whenever the
  // machine was busy, so the copy never refreshed. It runs in the background.
  const read = await readProjection(["decisions", "standing-law", "--json"], { timeoutMs: 45_000 });
  return { model: buildStandingLaw(read.value as Json | null), read };
}

function ConflictStrip({ conflicts }: { conflicts: StandingLawConflict[] }) {
  if (!conflicts.length) return null;
  return (
    <section className="flex flex-col gap-2">
      <SectionHeader
        tone="crit"
        icon={<GavelIcon />}
        label="Contradictions"
        sub="These proposed rules disagree with a rule in force."
        right={
          <span className="mono rounded-pill border border-crit-line bg-crit-soft px-2 py-0.5 text-2xs font-semibold text-crit tabular-nums">
            {conflicts.length}
          </span>
        }
      />
      <div className="flex flex-col gap-2">
        {conflicts.map((c) => (
          <div
            key={c.candidateId}
            className="flex flex-col gap-2 rounded-lg border border-crit-line bg-crit-soft/60 p-4"
          >
            <div className="text-sm font-semibold leading-snug text-ink">{c.restatement || c.verbatim}</div>
            {c.verbatim ? (
              <blockquote className="border-l-2 border-crit-line pl-3 text-xs italic text-muted">
                “{c.verbatim}”
              </blockquote>
            ) : null}
            <div className="mono flex flex-wrap items-center gap-x-3 gap-y-1 text-2xs text-faint">
              {c.conflictingTitle ? (
                <span className="not-italic text-crit">
                  conflicts with: <span className="font-semibold">{c.conflictingTitle}</span>
                </span>
              ) : null}
              {c.conflictsWithClaim ? <span>{c.conflictsWithClaim}</span> : null}
              {c.session ? <span>session {c.session}</span> : null}
              {c.line ? <span>L{c.line}</span> : null}
            </div>
          </div>
        ))}
      </div>
    </section>
  );
}

export default async function StandingLaw() {
  const [{ model, read }, { candidates: lawCandidates, read: candidateRead }] = await Promise.all([loadStandingLaw(), readStandingLawCandidates()]);
  const waiting = lawCandidates.length;
  const checking = read.value === null;
  // The proposed-rule count is known only once its read has a copy: a read
  // still loading or failed shows "—" and says so, never "0 waiting".
  const candidatesKnown = hasCopy(candidateRead);

  return (
    <div className="flex flex-col gap-6">
      <PendingRefresh pending={readsPending(read, candidateRead)} stamp={Date.now()} />
      <PageHeader
        name="Rules"
        lead="The rules every agent follows, and what you have allowed. Search here before deciding the same thing again."
        right={
          <>
            <span className="inline-flex items-center gap-1.5 rounded-pill border border-hair bg-soft-2 px-2.5 py-0.5 text-xs font-semibold text-muted">
              {checking ? "checking…" : <>{unit(model.counts.records, "rule")} in force</>}
            </span>
            <ReadChip read={read} />
          </>
        }
      />
      {checking && !model.records.length ? <div id="accepted-rules" tabIndex={-1} className="scroll-mt-6" /> : null}
      {waiting > 0 ? (
        <Link
          href={memoryReviewHref()}
          className="inline-flex items-center gap-2 rounded-lg border border-seam-line bg-seam-soft px-4 py-2.5 text-sm font-semibold text-seam transition-colors hover:bg-seam-soft/70 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-focus"
        >
          {unit(waiting, KNOWLEDGE_TERMS.proposedRule.name, KNOWLEDGE_TERMS.proposedRule.plural)} {waiting === 1 ? "waits" : "wait"} for your choice on the Memory page
          <span aria-hidden>→</span>
        </Link>
      ) : null}

      {checking && model.records.length === 0 ? (
        <ReadNotice read={read} label="the rules in force" />
      ) : (
        <>
          <ReadFailedBanner read={read} label="the rules in force" />
          <ConflictStrip conflicts={model.conflicts} />

          <section id="accepted-rules" tabIndex={-1} className="scroll-mt-6 flex flex-col gap-3">
            <SectionHeader
              tone="seam"
              icon={<GavelIcon />}
              label="In force"
              sub="Newest first. To change or retire a rule, ask an agent to record a new decision that replaces it; the old rule then shows as replaced."
            />
            {model.counts.duplicates > 0 ? (
              <p className="text-2xs text-faint">
                {unit(model.counts.duplicates, "decision")} the source listed twice {model.counts.duplicates === 1 ? "is" : "are"} shown once.
              </p>
            ) : null}
            <StandingLawBrowser records={model.records} byKind={model.counts.byKind} />
          </section>
        </>
      )}

      <section id="proposed-rules" tabIndex={-1} className="scroll-mt-6 flex flex-col gap-3">
        <SectionHeader
          tone="seam"
          icon={<GavelIcon />}
          label="Proposed rules waiting for you"
          sub={`${KNOWLEDGE_TERMS.proposedRule.means} You choose on the Memory page.`}
          right={
            <span className="text-2xs text-faint tabular-nums">
              {candidatesKnown ? `${waiting.toLocaleString("en-US")} waiting` : "— waiting"}
            </span>
          }
        />
        {!candidatesKnown ? (
          <ReadNotice read={candidateRead} label="proposed rules" />
        ) : (
          <>
            <ReadFailedBanner read={candidateRead} label="proposed rules" />
            {lawCandidates.length ? (
              <div className="flex flex-col gap-2.5">
                {lawCandidates.map((candidate) => (
                  <StandingLawCandidateCard key={candidate.id} candidate={candidate} />
                ))}
              </div>
            ) : (
              <EmptyState>No proposed rules wait for you.</EmptyState>
            )}
          </>
        )}
      </section>
    </div>
  );
}
