import { AdjudicationCard } from "@/components/memory/AdjudicationCard";
import { EvidenceCard } from "@/components/memory/EvidenceCard";
import { MemoryGraphPanel } from "@/components/memory/MemoryGraphPanel";
import { buildAdjudicationModel, type AdjudicationCardModel } from "@/components/memory/model";
import { PendingRefresh } from "@/components/PendingRefresh";
import { BrainIcon, DatabaseIcon, DocIcon, EmptyState, hasCopy, PageHeader, ReadChip, ReadFailedBanner, ReadNotice, readsPending, SectionHeader } from "@/components/kit";
import { consoleMutationsEnabled } from "@/lib/console-mutations";
import { countProposalRecords } from "@/lib/counts";
import { computeConsoleMutationReadiness } from "@/lib/instance-pin";
import { readProposedMemory, readStandingLawCandidates } from "@/lib/memory-data";
import { loadMemoryEgoGraph, type EgoGraph } from "@/lib/map/memory-graph";
import Link from "next/link";

export const dynamic = "force-dynamic";

/*
 * Memory — "What does the system believe, what's contested, what needs
 * adjudication?" One column, three honest bands:
 *   1) the adjudication queue (the operator's actual job — governed verdicts);
 *   2) the belief graph, embedded and seedable from any card (the /map/memory
 *      route is retired into this section);
 *   3) what's already canon (a tabular state strip) + the proposed source
 *      evidence that is deliberately kept OUT of the approval queue.
 *
 * Every read goes through the non-blocking read queue (`readProjection`):
 * the page renders at once from the last good copy, says "reading for the
 * first time" when there is none, and warns only when a refresh failed or
 * the copy is older than the warning age. A copy past the 25-second cache
 * window is still the last good read, not a fault, so the page no longer
 * flips to a warning between auto-refreshes.
 */

const STAT_STATES: { key: string; label: string }[] = [
  { key: "promoted", label: "Accepted for recall" },
  { key: "parked", label: "Set aside" },
  { key: "rejected", label: "Rejected" },
  { key: "no_promote", label: "Kept out of recall" },
  { key: "proposed", label: "Awaiting review" },
];

/** No copy yet: the loading or failed notice. A copy whose refresh failed: the banner above it. */
function MemoryRead({ read, label }: { read: Parameters<typeof ReadNotice>[0]["read"]; label: string }) {
  return hasCopy(read) ? <ReadFailedBanner read={read} label={label} /> : <ReadNotice read={read} label={label} />;
}

export default async function Memory({
  searchParams,
}: {
  searchParams: Promise<{ graph?: string }>;
}) {
  const { graph: graphParam } = await searchParams;
  const seed = (graphParam ?? "").trim();

  const [{ actionable, sourceEvidence, read: proposedRead }, law, ego] = await Promise.all([
    readProposedMemory(),
    readStandingLawCandidates(),
    seed ? loadMemoryEgoGraph(seed, { maxNodes: 60 }) : Promise.resolve<EgoGraph | null>(null),
  ]);

  const mutationsEnabled = computeConsoleMutationReadiness(consoleMutationsEnabled()).ready;
  // Parked accepted decisions (standing-law candidates) are durable verdicts the
  // operator owes just like proposed memory — they lead the queue so the clean
  // seam can never lie while any waits. Non-law parked rows (the 28k-row
  // knowledge corpus) are filtered out at the loader, so they never flood in.
  const lawModels = law.candidates
    .map((candidate) => buildAdjudicationModel(candidate, "standing-law-candidate"))
    .filter((m): m is AdjudicationCardModel => Boolean(m));
  const proposedModels = actionable
    .map((candidate) => buildAdjudicationModel(candidate))
    .filter((m): m is AdjudicationCardModel => Boolean(m));
  const models = [...lawModels, ...proposedModels];
  // The queue is known only when both reads have a copy; a part-read queue
  // never claims a total or an empty state.
  const queueKnown = hasCopy(proposedRead) && hasCopy(law.read);
  const queueClean = queueKnown && !proposedRead.error && !law.read.error;
  const counts = hasCopy(proposedRead) ? countProposalRecords(proposedRead.value) : null;

  return (
    <div className="flex flex-col gap-8">
      <PendingRefresh pending={readsPending(proposedRead, law.read)} stamp={Date.now()} />
      <PageHeader
        name="Memory"
        action={queueClean && !models.length ? { label: "Explore supporting evidence", href: "#memory-evidence" } : undefined}
        right={
          <>
            {queueKnown && models.length ? (
              <span className="inline-flex items-center gap-1.5 rounded-pill border border-seam-line bg-seam-soft px-2.5 py-0.5 text-xs font-semibold text-seam">
                <span className="mono tabular-nums">{models.length}</span> awaiting your verdict
              </span>
            ) : null}
            <ReadChip read={proposedRead} />
          </>
        }
      />
      <p className="text-sm text-muted" style={{ maxWidth: "var(--read-max)" }}>
        Decide what future agents may rely on. A proposed statement stays out of recall until accepted; setting it aside preserves it for later.
      </p>
      <nav aria-label="Memory next steps" className="flex flex-wrap gap-x-5 gap-y-2 text-xs font-semibold text-work">
        <Link href="/standing-law" className="hover:underline">Read standing rules →</Link>
        <Link href="/sources" className="hover:underline">Manage evidence sources →</Link>
        <Link href="/reviews" className="hover:underline">Review decisions →</Link>
      </nav>

      {/* 1 — adjudication queue (the operator's gate) */}
      <section id="memory-review" tabIndex={-1} className="scroll-mt-6 flex flex-col gap-3">
        <SectionHeader
          tone="memory"
          icon={<BrainIcon />}
          label="Memories to review"
          sub="Choose what to keep, set aside or reject."
        />
        <MemoryRead read={law.read} label="proposed rules" />
        <MemoryRead read={proposedRead} label="proposed memories" />
        {models.length ? (
          <div className="flex flex-col gap-3">
            {models.map((model) => (
              <AdjudicationCard key={model.id} model={model} mutationsEnabled={mutationsEnabled} />
            ))}
          </div>
        ) : queueClean ? (
          <EmptyState>
            No proposed memories need review. <Link href="/standing-law" className="font-semibold text-work hover:underline">Read the rules already guiding agents →</Link>
          </EmptyState>
        ) : null}
      </section>

      {/* 2 — the belief graph (retired /map/memory lives here) */}
      <MemoryGraphPanel seed={seed} graph={ego} open={Boolean(seed)} />

      {/* 3 — what's already believed: tabular state strip + proposed evidence */}
      <section className="flex flex-col gap-3">
        <SectionHeader
          tone="memory"
          icon={<DatabaseIcon />}
          label="Proposal history"
          sub="Counts of candidate records by their recorded state. Accepted records may be recalled when relevant; set-aside records are evidence for later."
        />
        <div className="grid grid-cols-2 gap-px overflow-hidden rounded-lg border border-hair bg-hair sm:grid-cols-5">
          {STAT_STATES.map((s) => (
            <div key={s.key} className="flex flex-col gap-1 bg-panel px-4 py-3">
              <span className="text-2xs font-bold uppercase tracking-wide text-faint">{s.label}</span>
              <span className="text-xl font-extrabold tabular-nums text-ink">{counts ? (counts[s.key] ?? 0) : "—"}</span>
            </div>
          ))}
        </div>
      </section>

      <section id="memory-evidence" tabIndex={-1} className="scroll-mt-6 flex flex-col gap-3">
        <SectionHeader
          tone="neutral"
          icon={<DocIcon />}
          label="Source evidence"
          sub="Supporting information for future decisions"
        />
        {sourceEvidence.length ? (
          <div className="grid gap-3 lg:grid-cols-2">
            {sourceEvidence.slice(0, 24).map((candidate) => (
              <EvidenceCard key={candidate.id} candidate={candidate} />
            ))}
          </div>
        ) : hasCopy(proposedRead) && !proposedRead.error ? (
          <EmptyState>No new supporting evidence has been recorded. <Link href="/sources" className="font-semibold text-work hover:underline">Check evidence sources →</Link></EmptyState>
        ) : !hasCopy(proposedRead) ? (
          <p className="text-sm text-muted">Shown when the proposed memories above have been read.</p>
        ) : null}
        {sourceEvidence.length > 24 ? (
          <p className="mono text-2xs text-faint">+{sourceEvidence.length - 24} more not shown.</p>
        ) : null}
      </section>
    </div>
  );
}
