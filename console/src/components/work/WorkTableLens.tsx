"use client";

import { useMemo, useState } from "react";

import { EmptyState, IdRef, StateBadge } from "@/components/kit";
import type { WorkLensItem } from "@/lib/work-views";

const FILTERS = ["all", "running", "blocked", "verifying"] as const;

export function WorkTableLens({ items, onOpenTrace }: { items: WorkLensItem[]; onOpenTrace: (workId: string) => void }) {
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<(typeof FILTERS)[number]>("all");
  const rows = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return items.filter((item) => {
      const state = item.runStatus || item.state;
      const filterMatch = filter === "all" || (filter === "blocked" ? ["blocked", "held"].includes(item.state) : state === filter || item.state === filter);
      const queryMatch = !needle || `${item.title} ${item.workId} ${item.owner} ${item.laneLabel} ${item.harness}`.toLowerCase().includes(needle);
      return filterMatch && queryMatch;
    });
  }, [filter, items, query]);

  return (
    <section className="min-w-0 overflow-hidden rounded-xl border border-hair bg-panel shadow-sm">
      <div className="flex flex-wrap items-center gap-2 border-b border-hair px-3 py-3">
        <div className="flex items-center gap-1 rounded-lg border border-hair bg-soft p-1" aria-label="Filter work by state">
          {FILTERS.map((value) => (
            <button key={value} type="button" aria-pressed={filter === value} onClick={() => setFilter(value)} className="rounded-md px-2.5 py-1 text-xs font-semibold capitalize text-faint hover:text-ink aria-pressed:bg-panel aria-pressed:text-ink aria-pressed:shadow-sm">
              {value}
            </button>
          ))}
        </div>
        <label className="ml-auto min-w-[220px] flex-1 sm:max-w-xs">
          <span className="sr-only">Filter work</span>
          <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Find work, owner, or harness…" className="h-8 w-full rounded-md border border-hair bg-soft px-3 text-xs text-ink outline-none placeholder:text-faint focus:border-work-line" />
        </label>
      </div>

      {rows.length ? (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[920px] border-collapse text-left">
            <thead><tr className="border-b border-hair bg-soft/70 text-2xs font-bold uppercase tracking-wide text-faint">
              <th className="px-3 py-2.5">Work item</th><th className="px-3 py-2.5">Execution</th><th className="px-3 py-2.5">Status</th><th className="px-3 py-2.5">Owner</th><th className="px-3 py-2.5">Next</th>
            </tr></thead>
            <tbody>
              {rows.map((item) => (
                <tr key={item.workId} className="group cursor-pointer border-b border-hair last:border-b-0 hover:bg-soft" tabIndex={0} role="button" aria-label={`Open trace for ${item.title}`} onClick={() => onOpenTrace(item.workId)} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); onOpenTrace(item.workId); } }}>
                  <td className="max-w-md px-3 py-3"><div className="line-clamp-2 text-sm font-semibold leading-snug text-ink">{item.title}</div><div className="mt-1"><IdRef id={item.workId} kind="work" plain truncate /></div></td>
                  <td className="px-3 py-3"><div className="text-xs font-semibold text-ink">{item.laneLabel}</div><div className="mt-0.5 text-2xs text-faint" title={item.harness ? `Recorded harness: ${item.harness}` : undefined}>{item.harnessLabel}</div></td>
                  <td className="px-3 py-3"><StateBadge state={item.runStatus || item.state} /><div className="mt-1 text-2xs tabular-nums text-faint">{item.ageLabel}</div></td>
                  <td className="max-w-44 px-3 py-3"><div className="truncate text-xs text-muted" title={item.owner}>{item.owner}</div>{item.actor ? <div className="mono mt-0.5 truncate text-2xs text-faint">run by {item.actor}</div> : null}</td>
                  <td className="max-w-xs px-3 py-3"><p className="line-clamp-2 text-xs leading-relaxed text-muted">{item.nextAction || "No next action projected."}</p></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : <div className="p-4"><EmptyState>No work matches this view.</EmptyState></div>}
      <div className="border-t border-hair px-3 py-2 text-2xs text-faint">Showing {rows.length} of {items.length} open WorkEnvelopes · table rows open the shared trace</div>
    </section>
  );
}
