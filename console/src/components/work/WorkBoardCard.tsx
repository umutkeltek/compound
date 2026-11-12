"use client";

import type { KeyboardEvent } from "react";

import { ClockIcon, IdRef, SegmentedProgress } from "@/components/kit";
import { hashToneFor, TONE_FILL } from "@/components/kit/tone";
import type { StateTone } from "@/components/kit/StateBadge";
import type { BoardCard } from "@/lib/work-pipeline";
import { appHue, humanizeProductId } from "@/lib/app-identity";

/**
 * One work card (DESIGN.md §3 work-card, restyled to the operator's board
 * reference): state chip + owner tag on top, title, the next step, the
 * envelope-stage progress, then owner and age. A traceable card (real
 * work_id) opens the Trace drawer on click or Enter/Space. Pure suggestions
 * (no work_id yet) render static — there is nothing to trace.
 */
const STATE_CHIP: Record<StateTone, { label: string; cls: string }> = {
  seam: { label: "Needs you", cls: "border-seam-line bg-seam-soft text-seam" },
  live: { label: "Running", cls: "border-work-line bg-work-soft text-work" },
  queued: { label: "Ready", cls: "border-hair bg-soft text-muted" },
  draft: { label: "Suggested", cls: "border-memory-line bg-memory-soft text-memory" },
  done: { label: "Done", cls: "border-good-line bg-good-soft text-good" },
  failed: { label: "Failed", cls: "border-crit-line bg-crit-soft text-crit" },
  dropped: { label: "Dropped", cls: "border-hair bg-soft text-faint" },
};

const STATE_WORD: Record<string, string> = {
  held: "Held",
  blocked: "Blocked",
  claimed: "Claimed",
  in_progress: "In progress",
  admitted: "Admitted",
  proposed: "Proposed",
};

function ownerLabel(owner: string): string {
  if (!owner) return "Unassigned";
  return owner.split(":")[0].replace(/_/g, " ");
}

function OwnerAvatar({ owner }: { owner: string }) {
  const tone = hashToneFor(owner || "unassigned");
  const letter = (ownerLabel(owner).charAt(0) || "?").toUpperCase();
  return (
    <span
      className={`inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-full text-2xs font-bold text-panel ${owner ? TONE_FILL[tone] : "bg-faint"}`}
      title={owner || "Unassigned"}
      aria-hidden
    >
      {letter}
    </span>
  );
}

export function WorkBoardCard({
  card,
  onOpen,
  compact = false,
}: {
  card: BoardCard;
  onOpen: (workId: string) => void;
  compact?: boolean;
}) {
  const interactive = card.traceable;
  const chip = card.state === "verifying"
    ? { label: "In review", cls: "border-route-line bg-route-soft text-route" }
    : STATE_CHIP[card.tone] ?? STATE_CHIP.queued;
  const app = card.surface.startsWith("product:") ? card.surface : "";
  const stateWord = STATE_WORD[card.state];
  const owner = card.subtitle;
  const overdue = card.tone === "seam" && /(\d+)\s*d/.test(card.ageLabel) && Number(card.ageLabel.match(/(\d+)\s*d/)?.[1]) >= 7;

  function open() {
    if (interactive) onOpen(card.workId);
  }
  function onKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (!interactive) return;
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      onOpen(card.workId);
    }
  }

  return (
    <div
      className={`group flex flex-col gap-2 rounded-xl border border-hair bg-panel p-3 shadow-sm transition-shadow ${
        interactive ? "cursor-pointer hover:border-faint hover:shadow" : ""
      }`}
      data-tone={card.tone}
      role={interactive ? "button" : undefined}
      tabIndex={interactive ? 0 : undefined}
      aria-label={interactive ? `Open trace ${card.title}` : undefined}
      onClick={open}
      onKeyDown={onKeyDown}
    >
      <div className="flex items-center gap-1.5">
        <span className={`inline-flex items-center rounded-md border px-1.5 py-0.5 text-2xs font-medium ${chip.cls}`}>
          {chip.label}
        </span>
        {stateWord && stateWord !== chip.label ? (
          <span className="inline-flex items-center rounded-md border border-hair px-1.5 py-0.5 text-2xs font-medium text-muted">{stateWord}</span>
        ) : null}
        {card.moveType ? (
          <span className="ml-auto truncate rounded-md border border-hair px-1.5 py-0.5 text-2xs font-medium text-muted">
            {card.moveType.replace(/_/g, " ")}
          </span>
        ) : null}
      </div>

      {app ? (
        <div className="flex min-w-0 items-center gap-1.5 text-xs text-muted">
          <span
            className="flex h-4 w-4 shrink-0 items-center justify-center rounded-full text-2xs font-bold text-white"
            style={{ backgroundColor: appHue(app) }}
            aria-hidden
          >
            {humanizeProductId(app).charAt(0)}
          </span>
          <span className="truncate" title={app}>{humanizeProductId(app)}</span>
        </div>
      ) : owner ? (
        <div className="flex min-w-0 items-center gap-1.5 text-xs text-muted">
          <span className={`h-2.5 w-2.5 shrink-0 rounded-full ${TONE_FILL[hashToneFor(owner)]}`} aria-hidden />
          <span className="truncate" title={owner}>{owner}</span>
        </div>
      ) : null}

      <span className="min-w-0 line-clamp-3 text-md font-semibold leading-snug text-ink" title={card.title}>
        {card.title}
      </span>

      {card.note ? (
        <p className={`text-sm leading-snug text-muted ${compact ? "line-clamp-2" : "line-clamp-3"}`}>{card.note}</p>
      ) : null}

      <SegmentedProgress
        className="mt-1"
        total={card.progress.total}
        done={card.progress.done}
        activeIndex={card.progress.active}
        activeTone={card.tone === "seam" ? "seam" : "work"}
        pulse={card.running}
        showCount={false}
      />

      <div className="mt-1 flex items-center gap-2 border-t border-hair-2 pt-2.5">
        <OwnerAvatar owner={owner} />
        {card.displayId && !compact ? (
          <span className="min-w-0 opacity-0 transition-opacity group-hover:opacity-100 group-focus-visible:opacity-100">
            <IdRef id={card.displayId} kind={card.traceable ? "work" : "plan"} plain truncate />
          </span>
        ) : null}
        {card.ageLabel ? (
          <span className={`ml-auto inline-flex shrink-0 items-center gap-1 text-xs tabular-nums ${overdue ? "text-crit" : "text-muted"}`}>
            <ClockIcon size={13} />
            {card.ageLabel}
          </span>
        ) : null}
      </div>

      {card.running ? <div className="runbar" aria-hidden /> : null}
    </div>
  );
}
