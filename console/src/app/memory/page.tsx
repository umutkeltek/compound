import { EvidenceCard } from "@/components/memory/EvidenceCard";
import { MemoryGraphPanel } from "@/components/memory/MemoryGraphPanel";
import { AdjudicationCard } from "@/components/memory/AdjudicationCard";
import { buildAdjudicationModel, type AdjudicationCardModel } from "@/components/memory/model";
import { PendingRefresh } from "@/components/PendingRefresh";
import { BrainIcon, DocIcon, EmptyState, hasCopy, PageHeader, ReadChip, ReadFailedBanner, ReadNotice, readsPending, SectionHeader } from "@/components/kit";
import { consoleMutationsEnabled } from "@/lib/console-mutations";
import { countAcceptedMemories, countProposalRecords, unit } from "@/lib/counts";
import { computeConsoleMutationReadiness } from "@/lib/instance-pin";
import { decideHref } from "@/lib/links";
import { readMemoryCanon, readProposedMemory, readStandingLawCandidates } from "@/lib/memory-data";
import { KNOWLEDGE_TERMS, memoryKindPlural, memoryRecordState, MEMORY_RECORD_STATE_ORDER } from "@/lib/plain-words";
import { loadMemoryEgoGraph, type EgoGraph } from "@/lib/map/memory-graph";
import Link from "next/link";

export const dynamic = "force-dynamic";

/*
 * Memory — "What does the system believe, what's contested, what needs
 * adjudication?" One column:
 *   0) what agents remember now: accepted memories by kind (`memory canon`),
 *      unknown (never 0) when that read fails;
 *   1) the adjudication queue (the operator's actual job — governed verdicts);
 *   2) the belief graph, embedded and seedable from any card (the /map/memory
 *      route is retired into this section);
 *   3) proposal records by state, folded, labelled as records (not memories),
 *      + the proposed source evidence kept OUT of the approval queue.
 *
 * Every read goes through the non-blocking read queue (`readProjection`):
 * the page renders at once from the last good copy, says "reading for the
 * first time" when there is none, and warns only when a refresh failed or
 * the copy is older than the warning age. A copy past the 25-second cache
 * window is still the last good read, not a fault, so the page no longer
 * flips to a warning between auto-refreshes.
 */

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

  const [{ actionable, sourceEvidence, read: proposedRead }, law, canonRead, ego] = await Promise.all([
    readProposedMemory(),
    readStandingLawCandidates(),
    readMemoryCanon(),
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
  const accepted = hasCopy(canonRead) ? countAcceptedMemories(canonRead.value) : null;
  const recordStates = counts
    ? [...MEMORY_RECORD_STATE_ORDER.filter((state) => state in counts), ...Object.keys(counts).filter((state) => !(MEMORY_RECORD_STATE_ORDER as readonly string[]).includes(state))]
    : [];

  return (
    <div className="flex flex-col gap-8">
      <PendingRefresh pending={readsPending(proposedRead, law.read, canonRead)} stamp={Date.now()} />
      <PageHeader
        name="Memory"
        lead="Agents keep notes from documents and past work. Before a note is given to future agents, you choose whether they may rely on it."
        action={queueClean && !models.length ? { label: "Explore supporting evidence", href: "#memory-evidence" } : undefined}
        right={
          <>
            {queueKnown && models.length ? (
              <span className="inline-flex items-center gap-1.5 rounded-pill border border-seam-line bg-seam-soft px-2.5 py-0.5 text-xs font-semibold text-seam">
                {unit(lawModels.length, "proposed rule")} and {unit(proposedModels.length, "proposed memory", "proposed memories")} wait for your choice
              </span>
            ) : null}
            <ReadChip read={proposedRead} />
          </>
        }
      />
      <nav aria-label="Memory next steps" className="flex flex-wrap gap-x-5 gap-y-2 text-xs font-semibold text-work">
        <Link href="/standing-law" className="hover:underline">Read the rules agents follow →</Link>
        <Link href="/sources" className="hover:underline">See where information comes from →</Link>
        <Link href={decideHref()} className="hover:underline">Other decisions waiting for you →</Link>
      </nav>

      {/* 0 — what agents remember now: the memories, not the proposal records */}
      <section id="memory-accepted" tabIndex={-1} className="scroll-mt-6 flex flex-col gap-2" aria-label="What agents remember">
        {accepted ? (
          <p className="text-sm leading-relaxed text-ink" style={{ maxWidth: "var(--read-max)" }}>
            Agents remember <span className="font-semibold tabular-nums">{unit(accepted.total, "accepted memory", "accepted memories")}</span>
            {accepted.byKind.length ? (
              <span className="text-muted">: {accepted.byKind.map((row) => `${row.count.toLocaleString("en-US")} ${memoryKindPlural(row.kind)}`).join(" · ")}</span>
            ) : null}
            .
          </p>
        ) : hasCopy(canonRead) ? (
          <p className="text-sm text-muted">How many memories agents have is unknown: the memory report came back without a count.</p>
        ) : canonRead.status === "unreachable" ? (
          <p className="text-sm text-muted">How many memories agents have is unknown: the memory report could not be read{canonRead.updating ? "; trying again now" : ""}.</p>
        ) : (
          <p className="text-sm text-muted">Checking how many memories agents have…</p>
        )}
        {accepted ? <ReadFailedBanner read={canonRead} label="the memory count" /> : null}
      </section>

      {/* 1 — adjudication queue (the operator's gate) */}
      <section id="memory-review" tabIndex={-1} className="scroll-mt-6 flex flex-col gap-3">
        <SectionHeader
          tone="memory"
          icon={<BrainIcon />}
          label="Waiting for your choice"
          sub={`Proposed rules first, then proposed memories. ${KNOWLEDGE_TERMS.proposedRule.means} ${KNOWLEDGE_TERMS.proposedMemory.means}`}
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
            No proposed memories or proposed rules wait for you. <Link href="/standing-law" className="font-semibold text-work hover:underline">Read the rules agents follow →</Link>
          </EmptyState>
        ) : null}
      </section>

      {/* 2 — the belief graph (retired /map/memory lives here) */}
      <MemoryGraphPanel seed={seed} graph={ego} open={Boolean(seed)} />

      {/* 3 — proposal records by state (a different population from the memories above) */}
      <details className="rounded-lg border border-hair bg-panel px-4 py-3">
        <summary className="cursor-pointer text-sm font-semibold text-ink">
          Proposal records by state
          <span className="ml-2 text-xs font-normal text-muted">{counts ? unit(Object.values(counts).reduce((sum, n) => sum + n, 0), "record") : "unknown"}</span>
        </summary>
        <p className="mt-2 text-xs leading-relaxed text-muted" style={{ maxWidth: "var(--read-max)" }}>
          Each record is one proposed statement and what was decided about it. These are not memories: a memory can also be recorded directly, without a proposal, so these numbers do not add up to the memories above. Proposed rules waiting for you are set-aside records.
        </p>
        {counts ? (
          <dl className="mt-3 grid gap-px overflow-hidden rounded-md border border-hair bg-hair sm:grid-cols-3">
            {recordStates.map((state) => {
              const words = memoryRecordState(state);
              return (
                <div key={state} className="flex flex-col gap-0.5 bg-panel px-3 py-2">
                  <dt className="text-xs font-semibold text-ink">{words.label}</dt>
                  <dd className="text-lg font-extrabold tabular-nums text-ink">{counts[state].toLocaleString("en-US")}</dd>
                  {words.means ? <dd className="text-2xs text-muted">{words.means}</dd> : null}
                </div>
              );
            })}
          </dl>
        ) : (
          <p className="mt-3 text-sm text-muted">Unknown: {hasCopy(proposedRead) ? "the proposal report came back without counts." : proposedRead.status === "unreachable" ? "the proposal report could not be read." : "the proposal report is still being read."}</p>
        )}
      </details>

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
