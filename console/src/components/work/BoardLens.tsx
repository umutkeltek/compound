"use client";

import { useEffect, useMemo, useState } from "react";

import { SearchIcon } from "@/components/kit/icons";
import { WorkBoardCard } from "@/components/work/WorkBoardCard";
import { agentDisplayName, agentKey } from "@/lib/harness-label";
import type { ParsedWorkListParams } from "@/lib/links";
import type { BoardColumnModel } from "@/lib/work-pipeline";

/**
 * Board lens — the by-state board (the default Work view). Each column is a
 * soft tray with a coloured state dot and count; a card opens the preview,
 * its link opens the work page. The search text and the agent filter live in
 * the address (`q`, `owner`), so they survive reload and Back. Long columns
 * show the first cards and a "Show N more" control.
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

type BoardParams = Pick<ParsedWorkListParams, "owner" | "q">;

export function BoardLens({
  columns,
  openCount,
  onOpenTrace,
  params,
  onParamsChange,
}: {
  columns: BoardColumnModel[];
  /** open work items (the Done column is not open) */
  openCount: number;
  onOpenTrace: (workId: string) => void;
  params: BoardParams;
  onParamsChange?: (next: Partial<BoardParams>, mode: "push" | "replace") => void;
}) {
  const [query, setQueryState] = useState(params.q);
  useEffect(() => { setQueryState(params.q); }, [params.q]);
  const setQuery = (value: string) => {
    setQueryState(value);
    onParamsChange?.({ q: value }, "replace");
  };
  const owner = params.owner;
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});

  // One entry per agent, whatever the spelling on the record (lib/harness-label.ts).
  const owners = useMemo(() => {
    const counts = new Map<string, { name: string; n: number }>();
    for (const column of columns) for (const card of column.cards) {
      const key = agentKey(card.subtitle);
      const entry = counts.get(key) ?? { name: agentDisplayName(card.subtitle), n: 0 };
      entry.n += 1;
      counts.set(key, entry);
    }
    return [...counts.entries()].sort((a, b) => b[1].n - a[1].n);
  }, [columns]);

  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return columns.map((column) => ({
      ...column,
      cards: column.cards.filter((card) => {
        if (owner && agentKey(card.subtitle) !== owner) return false;
        if (!needle) return true;
        return `${card.title} ${card.note} ${card.subtitle} ${card.agentName ?? ""} ${card.projectName ?? ""} ${card.displayId}`.toLowerCase().includes(needle);
      }),
    }));
  }, [columns, owner, query]);

  const narrowed = !!(query.trim() || owner);
  const isDone = (key: string) => key === "done";
  const shownOpen = filtered.filter((column) => !isDone(column.key)).reduce((sum, column) => sum + column.cards.length, 0);
  const shownClosed = filtered.filter((column) => isDone(column.key)).reduce((sum, column) => sum + column.cards.length, 0);
  const clear = () => {
    setQueryState("");
    onParamsChange?.({ q: "", owner: "" }, "push");
  };

  return (
    <div className="flex min-w-0 flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <label className="flex min-w-56 flex-1 items-center gap-2 rounded-lg border border-hair bg-panel px-3 py-1.5 text-sm text-faint focus-within:border-faint sm:max-w-72 sm:flex-none">
          <SearchIcon size={14} />
          <input
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search work, apps or agents"
            className="w-full bg-transparent text-ink outline-none placeholder:text-faint"
          />
        </label>
        <label className="inline-flex items-center gap-1.5 rounded-lg border border-hair bg-panel px-3 py-1.5 text-sm text-muted">
          <span className="text-faint">Agent:</span>
          <select
            value={owner}
            onChange={(event) => onParamsChange?.({ owner: event.target.value }, "push")}
            className="bg-transparent font-medium text-ink outline-none"
          >
            <option value="">Everyone</option>
            {owners.map(([key, entry]) => (
              <option key={key} value={key}>
                {entry.name} ({entry.n})
              </option>
            ))}
          </select>
        </label>
        {narrowed ? (
          <button type="button" onClick={clear} className="text-sm font-semibold text-work hover:underline">Clear search</button>
        ) : null}
        <span className="ml-auto text-sm tabular-nums text-faint">
          {narrowed
            ? `${shownOpen.toLocaleString("en-US")} of ${openCount.toLocaleString("en-US")} open match`
            : `${openCount.toLocaleString("en-US")} open`}
          {shownClosed ? ` · ${shownClosed.toLocaleString("en-US")} recently closed` : ""}
        </span>
      </div>

      {narrowed && shownOpen + shownClosed === 0 ? (
        <p className="rounded-xl border border-dashed border-hair px-4 py-6 text-center text-sm text-muted">
          No work matches {query.trim() ? <>&ldquo;{query.trim()}&rdquo;</> : "this agent"}.{" "}
          <button type="button" onClick={clear} className="font-semibold text-work hover:underline">Clear search</button>
        </p>
      ) : null}

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
                  <p className="rounded-xl border border-dashed border-hair px-3 py-6 text-center text-sm text-faint">
                    {narrowed ? "No match in this column." : column.emptyCopy}
                  </p>
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
