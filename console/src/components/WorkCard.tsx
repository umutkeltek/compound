import Link from "next/link";

import { Chip, MoveChip, StateBadge } from "@/components/ui";
import { ago, str } from "@/lib/compound";
import { shortText, WorkCardData } from "@/lib/work-data";

const EDGE: Record<string, string> = {
  proposed: "#f59e0b",
  admitted: "#f59e0b",
  ready: "#9aa4b2",
  in_progress: "#3b82f6",
  running: "#3b82f6",
  verifying: "#3b82f6",
  needs_review: "#8b5cf6",
  completed: "#10b981",
  failed: "#ef4444",
  cancelled: "#9aa4b2",
};

function operatorNextAction(action: string): string {
  const normalized = action.toLowerCase();
  if (normalized.includes("claim before touching")) return "Ready to start.";
  if (normalized.includes("advance running")) return "Ready to run.";
  if (normalized.includes("advance verifying")) return "Waiting for run evidence or a check.";
  if (normalized.includes("close") || normalized.includes("completed")) return "Ready to finish with proof.";
  return action;
}

export function WorkCard({
  work,
  compact = false,
}: {
  work: WorkCardData;
  compact?: boolean;
}) {
  const state = str(work.state);
  const id = work.displayId;
  const title = work.packet?.title || str(work.canonical_surface) || str(work.success_predicate) || id;
  const objective = work.packet?.objective || str(work.desired_outcome) || str(work.success_predicate);
  const nextAction = str(work.next_action);

  return (
    <div
      className="card flex flex-col gap-3 rounded-xl p-3"
      style={{ borderLeft: `3px solid ${EDGE[state] ?? "#9aa4b2"}` }}
    >
      <div className="flex items-start justify-between gap-2">
        <Link href={`/work/${id}`} className="min-w-0">
          <span className="mono block truncate text-[11px] text-ink-3">{id}</span>
          <span className="mt-1 block text-[13px] font-semibold leading-snug text-ink hover:underline">
            {title}
          </span>
        </Link>
        <StateBadge state={state} />
      </div>
      {objective ? (
        <p className="text-[12px] leading-relaxed text-ink-2">
          {shortText(objective, compact ? 180 : 260)}
        </p>
      ) : null}
      <div className="flex flex-wrap items-center gap-1.5 text-[11.5px] text-ink-3">
        {str(work.claimed_by) || str(work.owner) ? (
          <Chip tone="violet">{str(work.claimed_by) || str(work.owner)}</Chip>
        ) : null}
        {str(work.claimed_at) ? <span>{ago(work.claimed_at)}</span> : null}
        <MoveChip move={str(work.move) || str(work.move_type)} />
      </div>
      {!compact && nextAction ? (
        <p className="rounded-lg bg-slate-50 px-3 py-2 text-[11.5px] leading-relaxed text-ink-3">
          {operatorNextAction(nextAction)}
        </p>
      ) : null}
      {!compact ? (
        <div className="flex justify-end border-t border-line pt-2">
          <Link
            href={`/work/${id}`}
            className="rounded-lg bg-slate-100 px-3 py-1.5 text-[12px] font-semibold text-slate-700 hover:bg-slate-200"
          >
            Open
          </Link>
        </div>
      ) : null}
    </div>
  );
}
