import Link from "next/link";

import type { AttentionTotals } from "@/lib/attention";
import { describeWaiting, unit, type WaitingOnYou } from "@/lib/counts";
import { decideHref, workItemHref, workListHref } from "@/lib/links";
import { HOW_DECISIONS_MOVE } from "@/lib/plain-words";

/** Where Decide's quiet lines sit, so Today can link straight to them. */
export const CLOSED_ANCHOR = "closed-without-you";
export const HOUSEKEEPING_ANCHOR = "agent-housekeeping";

/**
 * The quiet lines under the waiting count, shared by Today and Decide: what
 * an agent must word first, what closed without him (with its reasons on
 * Decide), what waits on others, and the skill proposals folded out of the
 * count. Then, once per page, how a decision moves today. Each line names
 * what it counts; a line the projection gave no number for is left out.
 */
export function WaitingNotes({
  waiting,
  totals,
  workTitles,
  detail,
}: {
  waiting: WaitingOnYou;
  totals: AttentionTotals | null;
  /** work id → title, when the page read the work list */
  workTitles?: ReadonlyMap<string, string>;
  /** Decide: open the reasons in place. Today: link to them. */
  detail: boolean;
}) {
  const words = describeWaiting(waiting);
  const linkClass = "font-semibold text-work hover:underline";
  const others = waiting.waitingOnOthers;
  const otherLinks = others && others.items > 0
    ? others.itemIds.length === 1
      ? <Link href={workItemHref(others.itemIds[0])} className={linkClass}>{workTitles?.get(others.itemIds[0]) || "Open the work item"} →</Link>
      : <Link href={workListHref({ filter: "blocked" })} className={linkClass}>See work on hold →</Link>
    : null;
  return (
    <div className="flex max-w-read flex-col gap-1.5 text-sm text-muted" data-waiting-notes="">
      {words.wording ? <p>{words.wording}</p> : null}
      {words.closedWithoutYou ? (
        detail && totals && totals.reasons.length ? (
          <details id={CLOSED_ANCHOR} className="group scroll-mt-20">
            <summary className="cursor-pointer list-none marker:hidden">
              {words.closedWithoutYou} <span className="font-semibold text-work">See why ›</span>
            </summary>
            <ul className="mt-2 grid gap-1.5 border-l-2 border-hair pl-3 text-xs">
              {totals.reasons.map((reason) => (
                <li key={reason.reason}>
                  <span className="font-semibold text-ink">{unit(reason.count, "record")}</span> · {reason.reasonText}
                  {reason.nextStep ? <span className="block text-faint">{reason.nextStep}</span> : null}
                </li>
              ))}
            </ul>
          </details>
        ) : (
          <p>
            {words.closedWithoutYou}{" "}
            {detail ? null : <Link href={`${decideHref()}#${CLOSED_ANCHOR}`} className={linkClass}>See why →</Link>}
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
