import Link from "next/link";
import type { ReactNode } from "react";

import { describeAgents, STALE_AFTER_DAYS, unit, type InProgressCount } from "@/lib/counts";
import { workListHref } from "@/lib/links";

import type { Roster } from "./agent-roster";

/**
 * The answer every page under Agents opens with: how many agents hold work,
 * how many work items are marked in progress and how that splits, and how
 * many have not moved in a week. The words are lib/counts.ts's
 * (`describeAgents`, `describeInProgress`), so Today, Working now, All
 * agents and Waiting on each other say the same thing; each number links to
 * the Work list that holds exactly those items.
 */

const linkClass = "font-semibold text-work hover:underline";

function Count({ n, href, children }: { n: number; href: string; children: ReactNode }) {
  return n > 0 ? <Link href={href} className={linkClass}>{children}</Link> : <>{children}</>;
}

/** "528 work items are marked in progress: 87 running, 61 picked up but not started, 380 waiting for a proof check." */
export function InProgressSentence({ c }: { c: InProgressCount }) {
  const parts = [
    { n: c.running, words: "running", href: workListHref({ filter: "running" }) },
    { n: c.pickedUp, words: "picked up but not started", href: workListHref({ filter: "claimed" }) },
    { n: c.checking, words: "waiting for a proof check", href: workListHref({ filter: "verifying" }) },
  ].filter((part) => part.n > 0);
  // Punctuation stays inside each link, so the sentence reads the same as describeInProgress.
  return (
    <>
      <span className="text-ink">
        <Count n={c.total} href={workListHref({ filter: "progress" })}>{unit(c.total, "work item")}</Count>{" "}
        {c.total === 1 ? "is" : "are"} marked in progress{parts.length ? ":" : "."}
      </span>
      {parts.map((part, index) => (
        <span key={part.words}>
          {" "}
          <Count n={part.n} href={part.href}>{part.n.toLocaleString("en-US")} {part.words}{index === parts.length - 1 ? "." : ","}</Count>
        </span>
      ))}
    </>
  );
}

/** "473 of them were picked up more than 7 days ago and are still not finished." with the number linked to that list. */
export function StaleSentence({ c }: { c: InProgressCount }) {
  if (c.total === 0) return null;
  const n = c.pickedUpOver7Days;
  if (n === null) return <>The work records carry no pick-up time, so how long they have been open is unknown.</>;
  if (n === 0) return <>All of them were picked up in the last {STALE_AFTER_DAYS} days.</>;
  return (
    <>
      <Link href={workListHref({ filter: "stale" })} className={linkClass}>{n.toLocaleString("en-US")} of them</Link>{" "}
      {n === 1 ? "was" : "were"} picked up more than {STALE_AFTER_DAYS} days ago and {n === 1 ? "is" : "are"} still not finished.
    </>
  );
}

export function AgentsAnswer({
  roster,
  runRecordsVisible,
  extra,
}: {
  roster: Roster;
  /** false: this console cannot read run records, so "running" is only the work record's word */
  runRecordsVisible: boolean;
  /** one more line, such as work waiting for your decision */
  extra?: ReactNode;
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <p>{describeAgents(roster.count)}</p>
      <p className="text-base text-muted"><InProgressSentence c={roster.inProgress} /></p>
      {roster.inProgress.total > 0 ? <p className="text-base text-seam"><StaleSentence c={roster.inProgress} /></p> : null}
      {!runRecordsVisible ? (
        <p className="text-sm text-muted">
          This page cannot see the live run records, so it cannot tell which of these an agent is running this minute.
          &quot;Running&quot; is what the work record says.
        </p>
      ) : null}
      {extra}
    </div>
  );
}
