"use client";

import Link from "next/link";
import type { CSSProperties } from "react";
import { useEffect, useMemo, useState } from "react";

import { EmptyState, IdRef, StateBadge } from "@/components/kit";
import { appSlug } from "@/lib/app-identity";
import { agentDisplayName } from "@/lib/harness-label";
import { appHref, workItemHref, type ParsedWorkListParams, type WorkFilter } from "@/lib/links";
import { filterWorkList } from "@/lib/work-list";
import { STALE_AFTER_DAYS } from "@/lib/work-state";
import type { WorkLensItem } from "@/lib/work-views";

/** The List's filters, in the board's words (lib/plain-words.ts). The URL value is the id. */
const FILTERS: ReadonlyArray<{ id: WorkFilter; label: string }> = [
  { id: "focus", label: "On hold or not moved" },
  { id: "blocked", label: "On hold" },
  { id: "stale", label: `Not moved in ${STALE_AFTER_DAYS} days` },
  { id: "progress", label: "In progress" },
  { id: "running", label: "Running" },
  { id: "claimed", label: "Picked up, not started" },
  { id: "verifying", label: "Waiting for a proof check" },
  { id: "picked", label: "Picked up" },
  { id: "queued", label: "Not picked up yet" },
  { id: "all", label: "All open" },
];
const PAGE_SIZE = 18;

function rowAccent(item: WorkLensItem): string {
  if (["blocked", "held"].includes(item.state)) return "var(--seam-fg)";
  if (item.stale) return "var(--crit-fg)";
  const state = item.state;
  if (state === "running") return "var(--work-fg)";
  if (state === "verifying") return "var(--memory-fg)";
  if (state === "failed") return "var(--crit-fg)";
  return "var(--hair)";
}

type ListParams = Pick<ParsedWorkListParams, "filter" | "owner" | "app" | "project" | "q">;

export function WorkTableLens({ items, onOpenTrace, params, groups, onParamsChange }: {
  items: WorkLensItem[];
  onOpenTrace: (workId: string) => void;
  params: ListParams;
  /** the "by app" groups, or null while not read: names the group a filter shows */
  groups: ReadonlyArray<{ id: string; name: string; kind: string }> | null;
  onParamsChange?: (next: Partial<ListParams>, mode: "push" | "replace") => void;
}) {
  const filter = params.filter;
  const [query, setQueryState] = useState(params.q);
  useEffect(() => { setQueryState(params.q); }, [params.q]);
  function setFilter(value: WorkFilter) {
    onParamsChange?.({ filter: value }, "push");
  }
  function setQuery(value: string) {
    setQueryState(value);
    onParamsChange?.({ q: value }, "replace");
  }
  const [limit, setLimit] = useState(PAGE_SIZE);
  const { rows, waitingForGroups } = useMemo(
    () => filterWorkList(items, { ...params, q: query }, { projectsKnown: groups !== null }),
    [groups, items, params, query],
  );
  const visibleRows = rows.slice(0, limit);
  const group = params.app
    ? groups?.find((g) => g.kind === "app" && appSlug(g.id).toLowerCase() === params.app.toLowerCase())
    : params.project ? groups?.find((g) => g.id === params.project) : undefined;
  const groupName = params.app || params.project ? group?.name ?? (params.app || params.project) : "";
  const agentName = params.owner ? agentDisplayName(params.owner) : "";

  useEffect(() => {
    setLimit(PAGE_SIZE);
  }, [filter, query, params.app, params.project, params.owner]);

  return (
    <section className="min-w-0 overflow-hidden rounded-xl border border-hair bg-panel shadow-sm">
      <div className="flex flex-wrap items-center gap-2 border-b border-hair px-3 py-3">
        <div className="flex flex-wrap items-center gap-1 rounded-lg border border-hair bg-soft p-1" aria-label="Filter work by state">
          {FILTERS.map((option) => (
            <button key={option.id} type="button" aria-pressed={filter === option.id} onClick={() => setFilter(option.id)} className="rounded-md px-2.5 py-1 text-xs font-semibold text-faint hover:text-ink aria-pressed:bg-work-soft aria-pressed:text-work aria-pressed:shadow-sm">
              {option.label}
            </button>
          ))}
        </div>
        <label className="ml-auto min-w-[220px] flex-1 sm:max-w-xs">
          <span className="sr-only">Filter work</span>
          <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Find a task, app or agent…" className="h-8 w-full rounded-md border border-hair bg-soft px-3 text-xs text-ink outline-none placeholder:text-faint focus:border-work-line" />
        </label>
      </div>
      {groupName || agentName ? (
        <div className="flex flex-wrap items-center gap-2 border-b border-hair bg-soft/60 px-3 py-2 text-xs text-muted">
          <span>
            Only work {groupName ? <>for <strong className="font-semibold text-ink">{groupName}</strong></> : null}
            {groupName && agentName ? " " : null}
            {agentName ? <>held by <strong className="font-semibold text-ink">{agentName}</strong></> : null}.
          </span>
          <button type="button" className="font-semibold text-work hover:underline" onClick={() => onParamsChange?.({ app: "", project: "", owner: "" }, "push")}>
            Show all work
          </button>
        </div>
      ) : null}

      {rows.length ? (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[920px] border-collapse text-left">
            <thead><tr className="border-b border-hair bg-soft/70 text-2xs font-bold uppercase tracking-wide text-faint">
              <th className="px-3 py-2.5">Work item</th><th className="px-3 py-2.5">App</th><th className="px-3 py-2.5">Status</th><th className="px-3 py-2.5">Who has it</th><th className="px-3 py-2.5">Next</th>
            </tr></thead>
            <tbody>
              {visibleRows.map((item) => (
                <tr
                  key={item.workId}
                  className="work-focus-row group cursor-pointer border-b border-hair last:border-b-0 hover:bg-work-soft/30"
                  style={{ "--row-accent": rowAccent(item) } as CSSProperties}
                  tabIndex={0}
                  role="button"
                  aria-label={`Preview ${item.title}`}
                  onClick={() => onOpenTrace(item.workId)}
                  onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); onOpenTrace(item.workId); } }}
                >
                  <td className="max-w-md px-3 py-3"><Link href={workItemHref(item.workId)} onClick={(event) => event.stopPropagation()} onKeyDown={(event) => event.stopPropagation()} className="line-clamp-2 text-sm font-semibold leading-snug text-ink hover:underline">{item.title}</Link><Link href={workItemHref(item.workId)} onClick={(event) => event.stopPropagation()} onKeyDown={(event) => event.stopPropagation()} className="mt-1 block w-fit hover:underline"><IdRef id={item.workId} kind="work" plain truncate /></Link></td>
                  <td className="max-w-40 px-3 py-3 text-xs">{item.projectKind === "app" ? <Link href={appHref(item.projectId)} onClick={(event) => event.stopPropagation()} className="font-medium text-ink hover:underline">{item.projectName}</Link> : item.projectKind === "none" ? <span className="text-faint">Not linked</span> : item.projectName ? <span className="text-muted">{item.projectName}</span> : <span className="text-faint">Not read yet</span>}</td>
                  <td className="px-3 py-3"><StateBadge state={item.state} /><div className={`mt-1 text-2xs tabular-nums ${item.stale ? "font-semibold text-crit" : "text-faint"}`}>{item.ageLabel}{item.stale ? " · not moved in a week" : ""}</div></td>
                  <td className="max-w-44 px-3 py-3"><div className="truncate text-xs font-medium text-ink" title={item.owner}>{item.ownerName}</div>{item.owner && item.owner !== "Unassigned" && item.owner !== item.ownerName ? <div className="mono mt-0.5 truncate text-2xs text-faint" title={item.owner}>{item.owner}</div> : null}{item.runStatus ? <div className="mt-1 text-2xs text-faint">Run status: {item.runStatus.replace(/_/g, " ")}</div> : null}</td>
                  <td className="max-w-xs px-3 py-3"><p className="line-clamp-2 text-xs leading-relaxed text-muted">{item.nextAction || "No next step recorded."}</p></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : waitingForGroups ? (
        <div className="p-4"><EmptyState>Which app each work item belongs to is still being read, so this list cannot be narrowed to {groupName} yet. It fills in by itself.</EmptyState></div>
      ) : <div className="p-4"><EmptyState>{query.trim() ? <>No work matches &ldquo;{query.trim()}&rdquo; here. </> : "No work in this list. "}<button type="button" className="font-semibold text-work underline" onClick={() => { setQueryState(""); onParamsChange?.({ q: "", filter: "all", app: "", project: "", owner: "" }, "push"); }}>Show all open work</button></EmptyState></div>}
      <div className="flex flex-wrap items-center justify-between gap-2 border-t border-hair px-3 py-2 text-2xs text-faint">
        <span>Showing {visibleRows.length} of {rows.length} matching · {items.length} open tasks</span>
        {visibleRows.length < rows.length ? (
          <button type="button" onClick={() => setLimit((current) => current + PAGE_SIZE)} className="rounded-md border border-hair bg-soft px-2.5 py-1 font-semibold text-work hover:bg-work-soft">
            Show {Math.min(PAGE_SIZE, rows.length - visibleRows.length)} more
          </button>
        ) : <span>Select a task to see its progress and next step.</span>}
      </div>
    </section>
  );
}
