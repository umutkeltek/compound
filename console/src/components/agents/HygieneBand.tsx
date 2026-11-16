import Link from "next/link";

import { ForAgents } from "@/components/settings/primitives";
import { STALE_AFTER_DAYS, unit } from "@/lib/counts";
import { workListHref } from "@/lib/links";

import type { LatestWork } from "./agent-roster";
import { LEASE_HISTORY_LABEL } from "./executor-model";

/**
 * Work that has not moved: in progress and picked up more than
 * STALE_AFTER_DAYS ago (lib/work-state.ts `isStale`, the one stale rule Work
 * and Today use). It lists the oldest items by title, each linked to its
 * page, links the full list, and says plainly that nothing moves them on its
 * own yet. It never asks the operator to "clear" anything: the console has
 * no way to. Lease files left by finished work are one quiet line; the
 * command that archives them sits behind "For agents".
 */
export function HygieneBand({
  staleCount,
  oldest,
  staleHistory,
}: {
  /** in progress and not moved in a week; null when no pick-up time is known */
  staleCount: number | null;
  /** the oldest of them, oldest first */
  oldest: LatestWork[];
  /** lease files left behind by finished work (executors counts.stale_leases) */
  staleHistory: number;
}) {
  const history = Math.max(0, staleHistory);
  return (
    <section className={`rounded-lg border px-4 py-4 ${staleCount ? "admission-seam border-seam-line bg-panel shadow-sm" : "border-hair bg-panel"}`}>
      <h2 className="text-md font-semibold text-ink">Work that has not moved</h2>
      {staleCount === null ? (
        <p className="mt-1 text-sm text-muted">The work records carry no pick-up time, so how long items have been held is unknown.</p>
      ) : staleCount === 0 ? (
        <p className="mt-1 text-sm text-muted">Every item in progress was picked up in the last {STALE_AFTER_DAYS} days.</p>
      ) : (
        <>
          <p className="mt-1 text-sm leading-relaxed text-muted">
            <Link href={workListHref({ filter: "stale" })} className="font-semibold text-seam hover:underline">{unit(staleCount, "work item")}</Link>{" "}
            {staleCount === 1 ? "was" : "were"} picked up more than {STALE_AFTER_DAYS} days ago and {staleCount === 1 ? "is" : "are"} still not finished.
            Nothing moves {staleCount === 1 ? "it" : "them"} on its own yet: each waits for the agent that holds it to finish it or put it back.
          </p>
          <ul className="mt-3 flex flex-col divide-y divide-hair">
            {oldest.map((work) => (
              <li key={work.workId} className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-0.5 py-1.5">
                <Link href={work.href} className="min-w-0 text-sm text-ink hover:underline">{work.title}</Link>
                <span className="shrink-0 text-2xs text-muted">{work.agentName} · picked up {work.pickedUpAgo || "at an unknown time"}</span>
              </li>
            ))}
          </ul>
          {staleCount > oldest.length ? (
            <Link href={workListHref({ filter: "stale" })} className="mt-2 inline-block text-xs font-semibold text-work hover:underline">
              See all {staleCount.toLocaleString("en-US")}, oldest first →
            </Link>
          ) : null}
        </>
      )}
      {history > 0 ? (
        <>
          <p className="mt-3 border-t border-hair pt-3 text-2xs leading-relaxed text-faint">
            {LEASE_HISTORY_LABEL}: {unit(history, "file")} left by finished work. They change nothing you see here; an agent archives them during upkeep.
          </p>
          <ForAgents cmds={["compound home archive-history --kind lease --dry-run", "compound home archive-history --kind lease"]} label="For agents: command" />
        </>
      ) : null}
    </section>
  );
}
