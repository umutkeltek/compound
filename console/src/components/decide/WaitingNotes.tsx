import Link from "next/link";

import type { AttentionCoveredBatch, AttentionTotals } from "@/lib/attention";
import { describeWaiting, setAsideGroups, unit, type WaitingOnYou } from "@/lib/counts";
import {
  DECIDE_COVERED_ANCHOR,
  DECIDE_SET_ASIDE_ANCHOR,
  decideCoveredHref,
  decideHref,
  decideSetAsideHref,
  workItemHref,
  workListHref,
} from "@/lib/links";
import { HOW_DECISIONS_MOVE, plainArea, sentence } from "@/lib/plain-words";

/** Where Decide's quiet lines sit, so Today can link straight to them. */
export const CLOSED_ANCHOR = DECIDE_SET_ASIDE_ANCHOR;
export const HOUSEKEEPING_ANCHOR = "agent-housekeeping";

/** A decision an agent must word first, as Decide lists it: its key and its kind in plain words. */
export type WordingLink = { key: string; label: string };

/** A standing rule in plain words: what it allows, and until when. */
function ruleWords(batch: AttentionCoveredBatch): string {
  const rules = batch.rules.map((rule) => {
    const what = rule.capability ? sentence(rule.capability.replace(/[_-]+/g, " ")).replace(/\.$/, "") : "an unnamed permission";
    const until = rule.expiresAt && !Number.isNaN(Date.parse(rule.expiresAt))
      ? ` until ${new Date(rule.expiresAt).toLocaleDateString("en-US", { month: "short", day: "numeric", year: "numeric", timeZone: "UTC" })}`
      : "";
    return `${what}${until}`;
  });
  return rules.length ? `Your rule: ${rules.join("; ")}.` : "The rule that covers them is not named.";
}

/**
 * The quiet lines under the waiting count, shared by Today and Decide: what
 * an agent must word first (each one linked), what a standing rule of his
 * already covers, what was set aside (both numbers, the reasons grouped so
 * each group adds up, open on Decide when asked for), what waits on others,
 * and the skill proposals folded out of the count. Then, once per page, how a
 * decision moves today. Each line names what it counts; a line the projection
 * gave no number for is left out.
 */
export function WaitingNotes({
  waiting,
  totals,
  workTitles,
  detail,
  wordingLinks = [],
  openSetAside = false,
}: {
  waiting: WaitingOnYou;
  totals: AttentionTotals | null;
  /** work id → title, when the page read the work list */
  workTitles?: ReadonlyMap<string, string>;
  /** Decide: open the reasons in place. Today: link to them. */
  detail: boolean;
  /** the decisions an agent must word first, in Decide's order */
  wordingLinks?: readonly WordingLink[];
  /** Decide: the set-aside reasons start open (`?open=set-aside`, Today's "See why") */
  openSetAside?: boolean;
}) {
  const words = describeWaiting(waiting);
  const linkClass = "font-semibold text-work hover:underline";
  const others = waiting.waitingOnOthers;
  const otherLinks = others && others.items > 0
    ? others.itemIds.length === 1
      ? <Link href={workItemHref(others.itemIds[0])} className={linkClass}>{workTitles?.get(others.itemIds[0]) || "Open the work item"} →</Link>
      : <Link href={workListHref({ filter: "blocked" })} className={linkClass}>See work on hold →</Link>
    : null;
  const groups = totals ? setAsideGroups(totals.reasons) : [];
  const batches = waiting.coveredByRule?.batches.filter((batch) => batch.count > 0) ?? [];
  return (
    <div className="flex max-w-read flex-col gap-1.5 text-sm text-muted" data-waiting-notes="">
      {words.wording ? (
        <p>
          {words.wording}
          {wordingLinks.length ? (
            <>
              {" "}See {wordingLinks.length === 1 ? "it" : "them"}:{" "}
              {wordingLinks.map((link, index) => (
                <span key={link.key}>
                  {index ? ", " : ""}
                  <Link href={decideHref({ decision: link.key })} className={linkClass}>{link.label}</Link>
                </span>
              ))}
              .
            </>
          ) : null}
        </p>
      ) : null}
      {words.coveredByRule ? (
        detail ? (
          <section id={DECIDE_COVERED_ANCHOR} className="scroll-mt-20" aria-label="What your rules cover">
            <p>{words.coveredByRule}</p>
            <ul className="mt-1 grid gap-1.5 border-l-2 border-hair pl-3 text-xs">
              {batches.map((batch) => (
                <li key={batch.key || batch.area}>
                  <span className="font-semibold text-ink">{plainArea(batch.area)}</span> · {unit(batch.count, "item")}. {ruleWords(batch)}
                  {batch.rules.length ? <span className="mono text-faint"> {batch.rules.map((rule) => rule.grantId).filter(Boolean).join(", ")}</span> : null}
                  {batch.nextStep ? <span className="block text-faint">{batch.nextStep}</span> : null}
                </li>
              ))}
            </ul>
          </section>
        ) : (
          <p>
            {words.coveredByRule}{" "}
            <Link href={decideCoveredHref()} className={linkClass}>See which →</Link>
          </p>
        )
      ) : null}
      {words.rulesUnread ? <p className="text-seam">{words.rulesUnread}</p> : null}
      {words.closedWithoutYou ? (
        detail && groups.length ? (
          <details id={CLOSED_ANCHOR} open={openSetAside} className="group scroll-mt-20">
            <summary className="cursor-pointer list-none marker:hidden">
              {words.closedWithoutYou} <span className="font-semibold text-work">See why ›</span>
            </summary>
            <div className="mt-2 grid gap-3 border-l-2 border-hair pl-3 text-xs">
              {groups.map((group) => (
                <div key={group.heading} data-set-aside-group={group.closed ? "closed" : "open"}>
                  <div className="font-semibold text-ink">{group.heading}: {unit(group.count, "record")}</div>
                  <ul className="mt-1 grid gap-1.5">
                    {group.reasons.map((reason) => (
                      <li key={reason.reason}>
                        <span className="font-semibold text-ink">{unit(reason.count, "record")}</span> · {reason.reasonText}
                        {reason.nextStep ? <span className="block text-faint">{reason.nextStep}</span> : null}
                      </li>
                    ))}
                  </ul>
                </div>
              ))}
            </div>
          </details>
        ) : (
          <p>
            {words.closedWithoutYou}{" "}
            {detail ? null : <Link href={decideSetAsideHref()} className={linkClass}>See why →</Link>}
          </p>
        )
      ) : null}
      {words.waitingOnOthers ? <p>{words.waitingOnOthers} {otherLinks}</p> : null}
      {words.housekeeping ? (
        <p id={detail ? HOUSEKEEPING_ANCHOR : undefined} className="text-faint">{words.housekeeping}</p>
      ) : null}
      <p className="text-ink">{HOW_DECISIONS_MOVE}</p>
    </div>
  );
}
