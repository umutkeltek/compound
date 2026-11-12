"use client";

import { useMemo, useState } from "react";

import { SearchIcon } from "@/components/kit/icons";
import { WorkBoardCard } from "@/components/work/WorkBoardCard";
import type { BoardColumnModel } from "@/lib/work-pipeline";

/**
 * Board lens — the by-state kanban (the default Work view). Each column is a
 * soft tray with a coloured state dot and count; cards open the one Trace
 * drawer. Search and owner filters narrow every column at once; long columns
 * show the first cards and a "Show N more" control so the board stays
 * scannable without hiding anything.
 */
const COLUMN_DOT: Record<string, string> = {
  draft: "bg-memory",
  queued: "bg-faint",
  live: "bg-work",
  seam: "bg-seam",
  done: "bg-good",
  failed: "bg-crit",
  dropped: "bg-hair",
};

const PAGE = 8;

function ownerKey(owner: string): string {
  return owner ? owner.split(":")[0] : "Unassigned";
}

export function BoardLens({
  columns,
  onOpenTrace,
}: {
  columns: BoardColumnModel[];
  onOpenTrace: (workId: string) => void;
}) {
  const [query, setQuery] = useState("");
  const [owner, setOwner] = useState("all");
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});

  const owners = useMemo(() => {
    const counts = new Map<string, number>();
    for (const column of columns) for (const card of column.cards) {
      const key = ownerKey(card.subtitle);
      counts.set(key, (counts.get(key) ?? 0) + 1);
    }
    return [...counts.entries()].sort((a, b) => b[1] - a[1]);
  }, [columns]);

  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return columns.map((column) => ({
      ...column,
      cards: column.cards.filter((card) => {
        if (owner !== "all" && ownerKey(card.subtitle) !== owner) return false;
        if (!needle) return true;
        return `${card.title} ${card.note} ${card.subtitle} ${card.displayId}`.toLowerCase().includes(needle);
      }),
    }));
  }, [columns, owner, query]);

  const shown = filtered.reduce((sum, column) => sum + column.cards.length, 0);

  return (
    <div className="flex min-w-0 flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <label className="flex min-w-56 flex-1 items-center gap-2 rounded-lg border border-hair bg-panel px-3 py-1.5 text-sm text-faint focus-within:border-faint sm:max-w-72 sm:flex-none">
          <SearchIcon size={14} />
          <input
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search work"
            className="w-full bg-transparent text-ink outline-none placeholder:text-faint"
          />
        </label>
        <label className="inline-flex items-center gap-1.5 rounded-lg border border-hair bg-panel px-3 py-1.5 text-sm text-muted">
          <span className="text-faint">Owner:</span>
          <select
            value={owner}
            onChange={(event) => setOwner(event.target.value)}
            className="bg-transparent font-medium text-ink outline-none"
          >
            <option value="all">Everyone</option>
            {owners.map(([key, count]) => (
              <option key={key} value={key}>
                {key.replace(/_/g, " ")} ({count})
              </option>
            ))}
          </select>
        </label>
        <span className="ml-auto text-sm tabular-nums text-faint">{shown} items</span>
      </div>

      <div className="board-scroll overflow-x-auto pb-2">
        <div className="flex min-w-max items-start gap-4">
          {filtered.map((column) => {
            const open = expanded[column.key] ?? false;
            const cards = open ? column.cards : column.cards.slice(0, PAGE);
            const hidden = column.cards.length - cards.length;
            return (
              <section key={column.key} className="flex w-76 shrink-0 flex-col gap-2.5 rounded-2xl border border-hair bg-soft p-2.5" aria-label={column.label}>
                <header className="flex items-center gap-2 px-1.5 pt-0.5" title={column.description}>
                  <span className={`h-2 w-2 rounded-full ${COLUMN_DOT[column.tone] ?? "bg-faint"}`} aria-hidden />
                  <h3 className="text-md font-medium text-ink">{column.label}</h3>
                  <span className="rounded-md bg-panel px-1.5 text-xs tabular-nums text-muted">{column.cards.length}</span>
                </header>
                {cards.length ? (
                  cards.map((card) => (
                    <WorkBoardCard key={`${column.key}:${card.key}`} card={card} onOpen={onOpenTrace} />
                  ))
                ) : (
                  <p className="rounded-xl border border-dashed border-hair px-3 py-6 text-center text-sm text-faint">{column.emptyCopy}</p>
                )}
                {hidden > 0 || open ? (
                  <button
                    type="button"
                    onClick={() => setExpanded((prev) => ({ ...prev, [column.key]: !open }))}
                    className="rounded-lg py-1.5 text-sm text-muted transition-colors hover:bg-panel hover:text-ink"
                  >
                    {open ? "Show fewer" : `Show ${hidden} more`}
                  </button>
                ) : null}
              </section>
            );
          })}
        </div>
      </div>
    </div>
  );
}
