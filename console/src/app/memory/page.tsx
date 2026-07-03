import { DecisionCard } from "@/components/DecisionCard";
import { Card, Chip, EmptyState, FreshnessChip, Stat } from "@/components/ui";
import { ago, arr, str, type Json } from "@/lib/compound";
import {
  loadActionableMemoryCandidates,
  loadMemoryCounts,
  loadSourceEvidenceCandidates,
  type MemoryCandidate,
} from "@/lib/memory-data";

export const dynamic = "force-dynamic";

const STATE_LABELS: { key: string; label: string; accent: "orange" | "green" | "blue" | "gray" | "violet" }[] = [
  { key: "proposed", label: "Proposed", accent: "orange" },
  { key: "promoted", label: "Promoted", accent: "green" },
  { key: "parked", label: "Parked", accent: "blue" },
  { key: "rejected", label: "Rejected", accent: "gray" },
  { key: "no_promote", label: "No-promote", accent: "violet" },
];

const DECISION_GUIDE = [
  {
    label: "Needs decision",
    body: "Only durable memory candidates appear here. These can change future recall.",
  },
  {
    label: "Source evidence",
    body: "Imported documents and extracted knowledge stay as evidence unless they earn canon status.",
  },
  {
    label: "Promote",
    body: "Use only with a basis: confirmation, curated page, decision record, runtime proof, or trusted import.",
  },
  {
    label: "Park",
    body: "Safe default. It keeps evidence searchable without entering ambient recall.",
  },
];

function objectValue(value: unknown): Json {
  return value && typeof value === "object" && !Array.isArray(value) ? (value as Json) : {};
}

function unique(values: string[]): string[] {
  return [...new Set(values.filter(Boolean))];
}

function sourceEvidenceTitle(candidate: MemoryCandidate): string {
  const q = objectValue(candidate.qualifiers);
  return (
    str(candidate.object) ||
    str(q.item_ref) ||
    str(q.canonical) ||
    str(candidate.entity) ||
    candidate.id
  );
}

function SourceEvidenceCard({ candidate }: { candidate: MemoryCandidate }) {
  const q = objectValue(candidate.qualifiers);
  const refs = unique([
    str(candidate.source_trace_ref),
    ...arr(candidate.evidence_refs).map(String),
    str(q.extraction_id),
    str(q.item_ref),
    str(q.source_ref),
  ]);
  const source = str(q.source) || "knowledge";
  const sourceKind = str(q.source_kind) || "source";
  const metadataField = str(q.metadata_field);

  return (
    <div className="rounded-xl border border-line bg-white px-4 py-3">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-1.5">
            <Chip tone={source === "knowledge_import" ? "gray" : "blue"}>{source}</Chip>
            <Chip tone="violet">{sourceKind}</Chip>
            {metadataField ? <Chip tone="gray">{metadataField}</Chip> : null}
          </div>
          <h3 className="mt-2 line-clamp-2 text-[13px] font-semibold leading-snug text-ink">
            {sourceEvidenceTitle(candidate)}
          </h3>
        </div>
        {str(candidate.observation_date) ? (
          <span className="shrink-0 text-[11px] text-ink-3">{ago(candidate.observation_date)}</span>
        ) : null}
      </div>

      <p className="mt-2 line-clamp-3 text-[12.5px] leading-relaxed text-ink-2">
        {str(candidate.predicate) ? `${str(candidate.predicate)}: ` : ""}
        {str(candidate.object) || "Source evidence row"}
      </p>

      <details className="mt-2">
        <summary className="cursor-pointer text-[11px] font-medium text-ink-3 hover:text-ink-2">
          refs · {refs.length || 0}
        </summary>
        {refs.length ? (
          <ul className="mt-1 flex flex-col gap-0.5">
            {refs.map((ref) => (
              <li key={ref} className="mono break-all text-[10.5px] text-ink-3">
                {ref}
              </li>
            ))}
          </ul>
        ) : (
          <p className="mt-1 text-[11px] text-ink-3">No source refs surfaced.</p>
        )}
      </details>
    </div>
  );
}

export default async function Memory() {
  const [actionable, sourceEvidence, counts] = await Promise.all([
    loadActionableMemoryCandidates(),
    loadSourceEvidenceCandidates("proposed"),
    loadMemoryCounts(),
  ]);
  const proposedTotal = counts.proposed ?? 0;

  return (
    <div className="flex flex-col gap-8">
      <div className="flex items-end justify-between gap-4">
        <div>
          <h1 className="text-[22px] font-semibold tracking-tight">Memory</h1>
          <p className="mt-0.5 text-[12.5px] text-ink-3">
            Canon changes first. Source evidence stays out of your approval queue.
          </p>
        </div>
        <FreshnessChip source="compound memory candidates --json" />
      </div>

      <div className="grid gap-4 md:grid-cols-3">
        <Stat label="Needs decision" value={actionable.length} accent="orange" glyph="!" sub="durable memory only" />
        <Stat label="Source evidence" value={sourceEvidence.length} accent="blue" glyph="E" sub="not approval debt" />
        <Stat label="Proposed total" value={proposedTotal} accent="violet" glyph="M" sub="raw candidate state" />
      </div>

      <Card title="States" accent="violet" glyph="M">
        <div className="flex flex-wrap gap-2">
          {STATE_LABELS.map((state) => (
            <Chip key={state.key} tone={state.accent}>
              {state.label}: {counts[state.key] ?? 0}
            </Chip>
          ))}
        </div>
      </Card>

      <Card title="Decision guide" accent="blue" glyph="?">
        <div className="grid gap-3 md:grid-cols-2">
          {DECISION_GUIDE.map((entry) => (
            <div key={entry.label} className="rounded-xl border border-line bg-white px-4 py-3">
              <div className="text-[12.5px] font-semibold text-ink">{entry.label}</div>
              <p className="mt-1 text-[12px] leading-relaxed text-ink-3">{entry.body}</p>
            </div>
          ))}
        </div>
      </Card>

      <Card
        title="Needs decision"
        accent="orange"
        glyph="!"
        right={<Chip tone={actionable.length ? "orange" : "green"}>{actionable.length} actionable</Chip>}
      >
        {actionable.length ? (
          <div className="grid gap-3 lg:grid-cols-2">
            {actionable.slice(0, 24).map((candidate) => (
              <DecisionCard key={candidate.id} area="memory-adjudicate" item={candidate} />
            ))}
          </div>
        ) : (
          <EmptyState>No durable memory candidates need a decision right now.</EmptyState>
        )}
      </Card>

      <Card
        title="Source evidence"
        accent="blue"
        glyph="E"
        right={<Chip tone="blue">{sourceEvidence.length} proposed evidence rows</Chip>}
      >
        {sourceEvidence.length ? (
          <div className="grid gap-3 lg:grid-cols-2">
            {sourceEvidence.slice(0, 24).map((candidate) => (
              <SourceEvidenceCard key={candidate.id} candidate={candidate} />
            ))}
          </div>
        ) : (
          <EmptyState>No proposed source evidence rows.</EmptyState>
        )}
      </Card>
    </div>
  );
}
