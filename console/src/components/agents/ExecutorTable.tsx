"use client";

import Link from "next/link";
import { useState } from "react";

import { AvatarTile } from "@/components/kit";

import type { RosterAgent } from "./agent-roster";

/**
 * Who holds what, one row per agent (components/agents/agent-roster.ts).
 * Every number is a link to the Work list of exactly those items, filtered
 * by the agent's key (`owner=`), so the count and the list agree. A row opens
 * its most urgent items by title, each linked to its page, and "+N more"
 * opens the rest. The raw names the records use stay small, for finding the
 * lane; nothing else internal is shown.
 */

const COLUMN_COUNT = 7;

function CountLink({ value, href, seam = false }: { value: number | null; href: string; seam?: boolean }) {
  if (value === null) return <td className="px-2 py-3 text-right align-top"><span className="text-2xs text-faint">unknown</span></td>;
  const tone = seam && value > 0 ? "font-semibold text-seam" : value > 0 ? "text-ink" : "text-faint";
  return (
    <td className="px-2 py-3 text-right align-top">
      {value > 0 ? (
        <Link href={href} className={`mono text-sm tabular-nums hover:underline ${tone}`}>{value.toLocaleString("en-US")}</Link>
      ) : (
        <span className={`mono text-sm tabular-nums ${tone}`}>0</span>
      )}
    </td>
  );
}

function ExpansionRow({ agent }: { agent: RosterAgent }) {
  const more = agent.items - agent.top.length;
  return (
    <tr className="border-b border-hair bg-soft">
      <td colSpan={COLUMN_COUNT} className="px-4 py-4">
        <ul className="flex flex-col gap-2">
          {agent.top.map((work) => (
            <li key={work.workId} className="rounded-md border border-hair bg-panel px-3 py-2">
              <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
                <Link href={work.href} className="text-sm font-medium text-ink hover:underline">{work.title}</Link>
                <span className={`text-2xs ${work.onHold || work.stale ? "text-seam" : "text-faint"}`}>
                  {work.stateLabel}
                  {work.pickedUpAgo ? ` · picked up ${work.pickedUpAgo}` : ""}
                  {work.stale ? " · not moved in a week" : ""}
                </span>
              </div>
              {work.next ? <p className="mt-1 text-2xs leading-relaxed text-muted">Next: {work.next}</p> : null}
              <p className="mono mt-0.5 text-2xs text-faint">{work.workId}</p>
            </li>
          ))}
        </ul>
        {more > 0 ? (
          <Link href={agent.href.all} className="mt-3 inline-block text-xs font-semibold text-work hover:underline">
            +{more.toLocaleString("en-US")} more: all {agent.items.toLocaleString("en-US")} in Work →
          </Link>
        ) : null}
      </td>
    </tr>
  );
}

function AgentRow({ agent }: { agent: RosterAgent }) {
  const [open, setOpen] = useState(false);
  const panelId = `agent-detail-${agent.key}`;
  return (
    <>
      <tr className="border-b border-hair transition-colors hover:bg-soft">
        <td className="px-2 py-3 align-top">
          <button
            type="button"
            aria-expanded={open}
            aria-controls={panelId}
            onClick={() => setOpen((prev) => !prev)}
            title={open ? "Hide its work" : "Show its most urgent work"}
            className="mono rounded-md border border-hair px-1.5 py-0.5 text-2xs font-semibold text-muted hover:bg-soft-2"
          >
            {open ? "−" : "+"}
          </button>
        </td>
        <td className="px-2 py-3 align-top">
          <div className="flex items-start gap-2.5">
            <AvatarTile name={agent.name} size="md" className="mt-0.5" />
            <div className="min-w-0">
              <Link href={agent.href.all} className="text-md font-semibold text-ink hover:underline">{agent.name}</Link>
              {agent.spellings.length ? (
                <div className="mono mt-0.5 truncate text-2xs text-faint" title={agent.spellings.join(", ")}>
                  {agent.spellings.slice(0, 2).join(", ")}{agent.spellings.length > 2 ? ` +${agent.spellings.length - 2}` : ""}
                </div>
              ) : null}
            </div>
          </div>
        </td>
        <CountLink value={agent.items} href={agent.href.all} />
        <CountLink value={agent.inProgress.total} href={agent.href.progress} />
        <CountLink value={agent.stale} href={agent.href.stale} seam />
        <CountLink value={agent.onHold} href={agent.href.onHold} seam />
        <td className="px-2 py-3 text-right align-top text-2xs text-muted">{agent.oldestPickUpAgo || "unknown"}</td>
      </tr>
      {open ? (
        <tr id={panelId}>
          <td colSpan={COLUMN_COUNT} className="p-0">
            <table className="w-full">
              <tbody>
                <ExpansionRow agent={agent} />
              </tbody>
            </table>
          </td>
        </tr>
      ) : null}
    </>
  );
}

export function ExecutorTable({ agents }: { agents: RosterAgent[] }) {
  const head = "px-2 py-2.5 text-2xs font-bold uppercase tracking-wide text-faint";
  return (
    <div className="overflow-x-auto rounded-lg border border-hair bg-panel shadow-sm">
      <table className="w-full border-collapse text-left">
        <thead>
          <tr className="border-b border-hair">
            <th className="px-2 py-2.5" aria-label="Show its work" />
            <th className={head}>Agent</th>
            <th className={`${head} text-right`}>Holds</th>
            <th className={`${head} text-right`}>In progress</th>
            <th className={`${head} text-right`}>Not moved in a week</th>
            <th className={`${head} text-right`}>Need you</th>
            <th className={`${head} text-right`}>Oldest pick-up</th>
          </tr>
        </thead>
        <tbody>
          {agents.map((agent) => <AgentRow key={agent.key} agent={agent} />)}
        </tbody>
      </table>
    </div>
  );
}
