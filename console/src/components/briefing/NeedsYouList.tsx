import Link from "next/link";

import { HeadlineAct } from "@/components/briefing/HeadlineAct";
import { domainForArea } from "@/components/briefing/primitives";
import type { AttentionGroup } from "@/lib/attention";
import type { HeadlineActDescriptor } from "@/lib/briefing";
import { isOld } from "@/lib/briefing";
import { unit } from "@/lib/counts";
import { headlineActIdentity } from "@/lib/governed-action-client";
import type { Json } from "@/lib/compound";
import { decisionHeadline } from "@/lib/decide-model";
import { decideHref } from "@/lib/links";
import { plainArea } from "@/lib/plain-words";

const DOT: Record<ReturnType<typeof domainForArea>, string> = {
  work: "bg-work",
  memory: "bg-memory",
  capability: "bg-capability",
  route: "bg-route",
  product: "bg-product",
};

/**
 * Today's answer: at most five decisions, most urgent first (the attention
 * projection's own order, never re-ranked here). Each row says what to do
 * and why in one line, then one way in: the one-click act when the owner
 * offers it for the first row, else "Open". The rest of the queue is one
 * link, not a second list.
 */
export function NeedsYouList({
  groups,
  total,
  act,
  mutationsEnabled,
  nowMs,
}: {
  groups: AttentionGroup[];
  /** every decision waiting (for the "N more" link) */
  total: number;
  /** the one-click act for the FIRST row, when the owner offers one */
  act: HeadlineActDescriptor | null;
  mutationsEnabled: boolean;
  /** the clock, passed in */
  nowMs?: number;
}) {
  const now = nowMs ?? Date.now();
  const shown = groups.slice(0, 5);
  const more = Math.max(0, total - shown.length);
  return (
    <section aria-label="What needs you" className="overflow-hidden rounded-xl border border-hair bg-panel shadow-sm">
      <ol className="divide-y divide-hair">
        {shown.map((group, index) => {
          // The same title, app, question and wait as Decide's row (one function).
          const headline = decisionHeadline(group as unknown as Json, {}, now);
          const href = decideHref({ decision: group.key });
          return (
            <li key={group.key || index} className="flex flex-col gap-2 px-4 py-3.5 sm:flex-row sm:items-start sm:gap-4">
              <span
                className="mono mt-0.5 hidden h-6 w-6 shrink-0 items-center justify-center rounded-full border border-hair bg-soft text-2xs font-semibold text-muted sm:inline-flex"
                aria-hidden
              >
                {index + 1}
              </span>
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-2xs text-faint">
                  <span className="inline-flex items-center gap-1.5 font-semibold uppercase tracking-wide text-muted">
                    <span className={`h-1.5 w-1.5 rounded-full ${DOT[domainForArea(group.area)]}`} aria-hidden />
                    {plainArea(group.area)}
                  </span>
                  {headline.about ? (
                    headline.about.href
                      ? <Link href={headline.about.href} className="font-semibold text-work hover:underline">{headline.about.label}</Link>
                      : <span className="font-semibold text-muted">{headline.about.label}</span>
                  ) : null}
                  {group.count > 1 ? <span className="tabular-nums">{unit(group.count, "item")}</span> : null}
                  {headline.waiting ? <span className={isOld(group.oldest_created_at) ? "text-seam" : ""}>waiting {headline.waiting}</span> : null}
                </div>
                <p className="mt-1 text-md font-semibold leading-snug text-strong">{headline.subject || headline.question || plainArea(group.area)}</p>
                {headline.subject && headline.question ? <p className="mt-0.5 text-sm leading-snug text-muted">{headline.question}</p> : null}
              </div>
              <div className="flex shrink-0 flex-wrap items-center gap-3 sm:pt-0.5">
                {index === 0 && act ? <HeadlineAct key={headlineActIdentity(act)} act={act} mutationsEnabled={mutationsEnabled} /> : null}
                <Link
                  href={href}
                  className="inline-flex min-h-9 items-center rounded-md border border-hair bg-panel px-3 text-sm font-semibold text-work hover:bg-soft focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-focus"
                >
                  Open
                </Link>
              </div>
            </li>
          );
        })}
      </ol>
      {more > 0 ? (
        <Link
          href={decideHref()}
          className="flex items-center justify-between gap-3 border-t border-hair bg-soft px-4 py-2.5 text-sm text-muted hover:text-ink"
        >
          <span>{unit(more, "more decision")} on Decide</span>
          <span aria-hidden className="text-work">→</span>
        </Link>
      ) : null}
    </section>
  );
}
