import { arr, str, type Json } from "@/lib/compound";
import { ago } from "@/lib/time";
import { IdRef } from "@/components/kit";
import type { MemoryCandidate } from "@/lib/memory-data";

/*
 * A proposed source-evidence row (knowledge imports / extractions). These stay
 * OUT of the adjudication queue on purpose — they are evidence, not approval
 * debt, and only earn canon by being cited into a promoted claim. Restyled onto
 * the token system (was the legacy SourceEvidenceCard on /memory).
 */

function objectValue(value: unknown): Json {
  return value && typeof value === "object" && !Array.isArray(value) ? (value as Json) : {};
}

function unique(values: string[]): string[] {
  return [...new Set(values.filter(Boolean))];
}

function title(candidate: MemoryCandidate): string {
  const q = objectValue(candidate.qualifiers);
  return str(candidate.object) || str(q.item_ref) || str(q.canonical) || str(candidate.entity) || candidate.id;
}

export function EvidenceCard({ candidate }: { candidate: MemoryCandidate }) {
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
    <div className="rounded-lg border border-hair bg-panel px-4 py-3">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="inline-flex items-center rounded-pill border border-hair bg-soft-2 px-2 py-0.5 text-2xs font-semibold text-muted">
              {source}
            </span>
            <span className="inline-flex items-center rounded-pill border border-memory-line bg-memory-soft px-2 py-0.5 text-2xs font-semibold text-memory">
              {sourceKind}
            </span>
            {metadataField ? (
              <span className="inline-flex items-center rounded-pill border border-hair bg-soft-2 px-2 py-0.5 text-2xs font-semibold text-muted">
                {metadataField}
              </span>
            ) : null}
          </div>
          <h3 className="mt-2 line-clamp-2 text-sm font-semibold leading-snug text-ink">{title(candidate)}</h3>
        </div>
        {str(candidate.observation_date) ? (
          <span className="shrink-0 text-2xs tabular-nums text-faint">{ago(candidate.observation_date)}</span>
        ) : null}
      </div>

      <p className="mt-2 line-clamp-3 text-sm leading-relaxed text-muted">
        {str(candidate.predicate) ? `${str(candidate.predicate)}: ` : ""}
        {str(candidate.object) || "Source evidence row"}
      </p>

      <div className="mt-2 flex items-center justify-between gap-2">
        <IdRef id={candidate.id} kind="candidate" />
        {refs.length ? (
          <details className="min-w-0">
            <summary className="cursor-pointer text-2xs font-medium text-faint hover:text-muted">
              refs · {refs.length}
            </summary>
            <ul className="mt-1 flex flex-col gap-0.5">
              {refs.map((ref) => (
                <li key={ref} className="mono break-all text-2xs text-faint">
                  {ref}
                </li>
              ))}
            </ul>
          </details>
        ) : null}
      </div>
    </div>
  );
}
