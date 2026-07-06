import Link from "next/link";

import { Chip, EmptyState, StateBadge } from "@/components/ui";
import { ago, arr, str, type Json } from "@/lib/compound";
import { shortText } from "@/lib/work-data";
import { shortBlockedRefs } from "@/lib/work-labels";

type ChangedEntry = {
  id: string;
  href?: string;
  headline: string;
  body: string;
  when?: string;
  actor?: string;
  proof?: string;
  status?: string;
  sortKey: number;
};

export type MoveTypeGroup = "outward" | "meaning" | "rules" | "upkeep";

export type MoveTypeMix = {
  groups: Record<MoveTypeGroup, number>;
  total: number;
  lowOutward: boolean;
};

function timestamp(value: unknown): number {
  const raw = str(value);
  if (!raw) return 0;
  if (/^\d+$/.test(raw)) {
    const n = Number(raw);
    if (n > 1e16) return n / 1e6;
    if (n > 1e13) return n / 1e3;
    if (n > 1e11) return n;
    return n * 1000;
  }
  const parsed = Date.parse(raw);
  return Number.isNaN(parsed) ? 0 : parsed;
}

function surfaceTitle(item: Json): string {
  const surface = str(item.canonical_surface);
  if (surface.startsWith("packet:")) {
    return surface.slice("packet:".length).replace(/-/g, " ");
  }
  return (
    str(item.action_context?.title) ||
    str(item.success_predicate) ||
    str(item.desired_outcome) ||
    str(item.work_id) ||
    str(item.id)
  );
}

function moveLabel(move: string): string {
  switch (move) {
    case "outward_delta":
      return "Product change";
    case "memory_delta":
      return "Memory change";
    case "rule_delta":
      return "Rule change";
    case "control_plane_repair":
      return "System repair";
    case "audit_only":
      return "Audit";
    default:
      return move ? move.replace(/_/g, " ") : "Change";
  }
}

export function moveTypeGroup(move: string): MoveTypeGroup {
  switch (move) {
    case "outward_delta":
      return "outward";
    case "memory_delta":
      return "meaning";
    case "rule_delta":
      return "rules";
    default:
      return "upkeep";
  }
}

export function summarizeMoveTypeMix(recent: Json[], now = Date.now()): MoveTypeMix {
  const groups: Record<MoveTypeGroup, number> = {
    outward: 0,
    meaning: 0,
    rules: 0,
    upkeep: 0,
  };
  const weekMs = 7 * 24 * 3600 * 1000;

  for (const item of recent) {
    const when = timestamp(item.claimed_at ?? item.created_at ?? item.generated_at);
    if (when > 0 && now - when > weekMs) continue;
    groups[moveTypeGroup(str(item.move_type) || str(item.move))]++;
  }

  const total = groups.outward + groups.meaning + groups.rules + groups.upkeep;
  return {
    groups,
    total,
    lowOutward: total > 0 && groups.outward <= Math.max(1, Math.floor(total * 0.15)),
  };
}

function terminalHeadline(item: Json): string {
  const title = shortText(surfaceTitle(item), 96);
  const move = moveLabel(str(item.move_type) || str(item.move));
  switch (str(item.state)) {
    case "completed":
      return `${move} recorded`;
    case "failed":
      return `Failed ${title}`;
    case "cancelled":
      return `Archived ${title}`;
    default:
      return `${move} recorded`;
  }
}

function terminalEntry(item: Json, index: number): ChangedEntry {
  const receipt = str(item.receipt_id) || str(item.receipt);
  const transition = str(item.transition_id) || str(item.transition);
  const actor = str(item.claimed_by) || str(item.owner);
  const title = shortText(surfaceTitle(item), 120);
  const evidenceCount = arr(item.evidence_refs).length;
  const proof = [receipt, transition ? `change ${transition}` : ""].filter(Boolean).join(" / ");
  const evidence = evidenceCount
    ? `${evidenceCount} evidence ref${evidenceCount === 1 ? "" : "s"}`
    : "";

  return {
    id: str(item.work_id) || str(item.id) || `recent-${index}`,
    href: str(item.work_id) ? `/work/${str(item.work_id)}` : undefined,
    headline: terminalHeadline(item),
    body: [title, evidence].filter(Boolean).join(". ") || "Proof recorded.",
    when: ago(str(item.claimed_at)),
    actor,
    proof,
    status: str(item.state),
    sortKey: timestamp(item.claimed_at) || 1_000_000 - index,
  };
}

function actionLabel(action: string, status?: string): string {
  switch (action) {
    case "admit":
      return "added to work";
    case "prepare":
      return "prepared";
    case "claim":
      return "started";
    case "advance":
      return status === "running" ? "started run" : "continued";
    case "close":
      return "finished";
    case "none":
      return "no safe move";
    default:
      return action ? action.replace(/_/g, " ") : "continued";
  }
}

function runEntry(run: Json, kind: "continue" | "dispatch", index: number): ChangedEntry {
  const steps = arr(run.steps);
  const stepActions = arr(run.step_actions).map(String);
  const stepWorkIDs = arr(run.step_work_ids).map(String).filter(Boolean);
  const firstWork = stepWorkIDs[0];
  const actions = steps.length
    ? steps.map((step) => actionLabel(str(step.action), str(step.status)))
    : stepActions.map((action) => actionLabel(action));
  const stepCount = Number(run.step_count) || actions.length || 1;
  const waitReason = str(run.wait_reason).replace(/_/g, " ");
  const blocked = shortBlockedRefs(arr(run.blocked_on).map(String), 2);
  const headline =
    kind === "continue"
      ? `Continued ${stepCount} safe step${stepCount === 1 ? "" : "s"}`
      : `Moved ${stepCount} queued step${stepCount === 1 ? "" : "s"}`;
  const bodyParts = [
    actions.length ? actions.join(" / ") : "",
    waitReason ? `now waiting for ${waitReason}` : "",
    blocked ? `waiting on ${blocked}` : "",
  ].filter(Boolean);

  return {
    id: str(run.id) || `${kind}-${index}`,
    href: firstWork ? `/work/${firstWork}` : undefined,
    headline,
    body: bodyParts.join("; ") || str(run.next_action) || "Safe lifecycle movement recorded.",
    when: ago(str(run.generated_at)),
    actor: str(run.actor),
    proof: str(run.id),
    status: str(run.status),
    sortKey: timestamp(run.generated_at) || 900_000 - index,
  };
}

export function ChangedProofStream({
  recent,
  continueRuns,
  dispatchStepRuns,
}: {
  recent: Json[];
  continueRuns: Json[];
  dispatchStepRuns: Json[];
}) {
  const entries = [
    ...continueRuns.map((run, index) => runEntry(run, "continue", index)),
    ...dispatchStepRuns.map((run, index) => runEntry(run, "dispatch", index)),
    ...recent.map(terminalEntry),
  ]
    .sort((a, b) => b.sortKey - a.sortKey)
    .slice(0, 10);

  if (!entries.length) return <EmptyState>No recent changes in the report.</EmptyState>;

  return (
    <ul className="flex flex-col gap-2">
      {entries.map((entry) => {
        const body = (
          <div className="flex min-w-0 flex-col gap-1">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-medium text-ink">{entry.headline}</span>
              {entry.status ? <StateBadge state={entry.status} /> : null}
              {entry.actor ? <Chip tone="violet">{entry.actor}</Chip> : null}
            </div>
            <div className="text-[12px] leading-relaxed text-ink-3">{entry.body}</div>
          </div>
        );

        return (
          <li key={entry.id} className="rounded-xl border border-line bg-white px-3 py-2.5">
            <div className="flex items-start justify-between gap-3">
              {entry.href ? (
                <Link href={entry.href} className="min-w-0 hover:underline">
                  {body}
                </Link>
              ) : (
                body
              )}
              <div className="flex shrink-0 flex-col items-end gap-1 text-right">
                {entry.when ? <span className="text-[11px] text-ink-3">{entry.when}</span> : null}
                {entry.proof ? <span className="mono text-[10.5px] text-ink-3">{entry.proof}</span> : null}
              </div>
            </div>
          </li>
        );
      })}
    </ul>
  );
}
