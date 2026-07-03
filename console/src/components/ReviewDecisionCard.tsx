import { ReviewDecisionForm } from "@/components/ReviewDecisionForm";
import { Chip } from "@/components/ui";
import { arr, str } from "@/lib/compound";
import { ExternalReviewRow, externalReviewCommand } from "@/lib/review-data";

export function ReviewDecisionCard({ row }: { row: ExternalReviewRow }) {
  const boundaries = arr(row.boundaries).map(String).filter(Boolean);
  const command = externalReviewCommand(row);
  const draft = row.draft;

  return (
    <div className="flex flex-col gap-3 rounded-xl border border-line p-4">
      <div className="flex flex-col gap-2">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h3 className="text-[16px] font-semibold leading-tight text-ink">
              {str(row.repo) || str(row.target_path) || str(row.id)}
            </h3>
            <div className="mt-1 flex flex-wrap items-center gap-1.5">
              {str(row.lane) ? <Chip tone="blue">{str(row.lane)}</Chip> : null}
              {str(row.owner) ? <Chip tone="violet">{str(row.owner)}</Chip> : null}
              <Chip tone="orange">{str(row.readiness) || str(row.status)}</Chip>
            </div>
          </div>
          <span className="mono text-[11px] text-ink-3">{str(row.id)}</span>
        </div>
        <div className="grid gap-2 md:grid-cols-2">
          <div className="rounded-lg bg-slate-50 px-3 py-2">
            <div className="section-label mb-1">What This Is</div>
            <p className="text-[12.5px] leading-relaxed text-ink-2">
              {str(row.next_action) || "No next action supplied."}
            </p>
          </div>
          <div className="rounded-lg bg-slate-50 px-3 py-2">
            <div className="section-label mb-1">Never</div>
            <p className="text-[12.5px] leading-relaxed text-ink-2">
              {str(row.not_to_do) || "No explicit boundary supplied."}
            </p>
          </div>
        </div>
        {boundaries.length ? (
          <details className="rounded-lg border border-line bg-white px-3 py-2">
            <summary className="cursor-pointer text-[12px] font-semibold text-ink-2">
              Boundaries ({boundaries.length})
            </summary>
            <ul className="mt-2 flex flex-col gap-1.5">
              {boundaries.map((boundary) => (
                <li key={boundary} className="text-[12px] leading-relaxed text-ink-3">
                  {boundary}
                </li>
              ))}
            </ul>
          </details>
        ) : null}
      </div>
      <ReviewDecisionForm
        reviewId={str(row.id)}
        command={command}
        defaultDecision={draft?.decision || str(row.decision) || "keep_needs_review"}
        defaultRationale={draft?.rationale || ""}
        defaultConfidence={draft?.confidence || String(row.graph_evidence_strength ?? "")}
      />
    </div>
  );
}
