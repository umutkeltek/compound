"use client";

import Link from "next/link";
import type { KeyboardEvent } from "react";

import { ClockIcon, IdRef, SegmentedProgress } from "@/components/kit";
import { hashToneFor, TONE_FILL } from "@/components/kit/tone";
import type { StateTone } from "@/components/kit/StateBadge";
import type { BoardCard } from "@/lib/work-pipeline";
import { appHue } from "@/lib/app-identity";
import { workItemHref } from "@/lib/links";
import { plainState } from "@/lib/plain-words";

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

function OwnerAvatar({ owner, name = "" }: { owner: string; name?: string }) {
  const tone = hashToneFor(owner || "unassigned");
  const letter = ((name || ownerLabel(owner)).charAt(0) || "?").toUpperCase();
  return (
    <span
      className={`inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-full text-2xs font-bold text-panel ${owner ? TONE_FILL[tone] : "bg-faint"}`}
      title={name ? `${name} · ${owner}` : owner || "Unassigned"}
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
  // The chip names the card's real state in plain words (lib/plain-words.ts):
  // a claimed card says "Picked up, not started", never "Running".
  const toneChip = card.state === "verifying"
    ? { label: "", cls: "border-route-line bg-route-soft text-route" }
    : card.state === "claimed"
      ? { label: "", cls: "border-hair bg-soft text-muted" }
      : STATE_CHIP[card.tone] ?? STATE_CHIP.queued;
  const chip = { cls: toneChip.cls, label: card.state ? plainState(card.state) : toneChip.label };
  // The app or codebase from the shared resolver; nothing when it names
  // neither or is not read yet (the List and "By app" say which).
  const appName = card.projectKind && card.projectKind !== "none" ? card.projectName ?? "" : "";
  // The chip already carries the exact state; the old second state tag
  // (STATE_WORD) only repeated it in system words.
  const stateWord = card.state ? "" : STATE_WORD[card.state] ?? "";
  const owner = card.subtitle;
  // Red age: on hold for a week or more, or in progress and not moved for a week (lib/work-state.ts).
  const overdue = card.stale || (card.tone === "seam" && /(\d+)\s*d/.test(card.ageLabel) && Number(card.ageLabel.match(/(\d+)\s*d/)?.[1]) >= 7);

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
      aria-label={interactive ? `Preview ${card.title}` : undefined}
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

      </div>

      {/* Which app it is for and who has it, on one line (lib/work-project.ts, lib/harness-label.ts). */}
      <div className="flex min-w-0 items-center gap-1.5 text-xs text-muted">
        {appName ? (
          <>
            <span
              className="flex h-4 w-4 shrink-0 items-center justify-center rounded-full text-2xs font-bold text-white"
              style={{ backgroundColor: appHue(card.projectId || appName) }}
              aria-hidden
            >
              {appName.charAt(0).toUpperCase()}
            </span>
            <span className="min-w-0 truncate font-medium text-ink" title={appName}>{appName}</span>
            <span className="text-faint" aria-hidden>·</span>
          </>
        ) : null}
        <span className="shrink-0 truncate" title={owner || "No agent"}>{card.agentName || "No agent"}</span>
        {card.lane ? <span className="mono min-w-0 truncate text-2xs text-faint" title={card.lane}>{card.lane}</span> : null}
      </div>

      <span className="min-w-0 line-clamp-3 text-md font-semibold leading-snug text-ink" title={card.title}>
        {card.title}
      </span>

      {card.repeatHint ? <p className="mono truncate text-2xs text-faint" title={card.repeatHint}>{card.repeatHint}</p> : null}

      {card.note && card.tone !== "done" ? (
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
        <OwnerAvatar owner={owner} name={card.agentName} />
        {card.traceable ? (
          <Link
            href={workItemHref(card.workId)}
            onClick={(event) => event.stopPropagation()}
            onKeyDown={(event) => event.stopPropagation()}
            className="min-w-0 truncate text-xs font-medium text-work hover:underline"
            title={card.workId}
          >
            Open work page
          </Link>
        ) : card.displayId && !compact ? (
          <span className="min-w-0 opacity-0 transition-opacity group-hover:opacity-100 group-focus-visible:opacity-100">
            <IdRef id={card.displayId} kind="plan" plain truncate />
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
