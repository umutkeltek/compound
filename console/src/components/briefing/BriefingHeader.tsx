import { PageHeader, ReadChip, ReadNotice, type ReadView } from "@/components/kit";
import type { WaitingOnYou } from "@/lib/attention";
import { str } from "@/lib/compound";
import { describeWaiting } from "@/lib/counts";
import { waitingAge } from "@/lib/decide-model";

const TZ = "Europe/Istanbul";

function greeting(now: Date): string {
  const hour = Number(new Intl.DateTimeFormat("en-US", { timeZone: TZ, hour: "numeric", hour12: false }).format(now));
  const part = hour < 12 ? "morning" : hour < 18 ? "afternoon" : "evening";
  const name = str(process.env.COMPOUND_OPERATOR_DISPLAY_NAME || process.env.COMPOUND_OPERATOR_NAME);
  return name ? `Good ${part}, ${name}` : `Good ${part}`;
}

function dateLabel(now: Date): string {
  return new Intl.DateTimeFormat("en-US", { timeZone: TZ, weekday: "long", month: "long", day: "numeric" }).format(now);
}


/**
 * Today's question and its answer, first on the page: "What needs you
 * today?" and one sentence built from the ONE "waiting for you" definition
 * (only what is his; Decide's header and the sidebar say the same number)
 * (lib/counts.ts → lib/attention.ts `waitingOnYou`) the sidebar, Work and
 * Live use, with the units named. An attention read that has not landed shows
 * its read state (being read / could not read), never zero or "nothing".
 */
export function BriefingHeader({
  now,
  waiting,
  read,
  oldest,
}: {
  now: Date;
  waiting: WaitingOnYou | null;
  read: ReadView;
  oldest: unknown;
}) {
  // The same function the oldest card's "waiting N days" uses, on the same clock.
  const age = waitingAge(oldest instanceof Date ? oldest.toISOString() : oldest, now.getTime());
  return (
    <PageHeader
      meta={`${greeting(now)} · ${dateLabel(now)}`}
      name="Today"
      action={waiting && waiting.groups === 0 ? { label: "See work in progress", href: "/live" } : undefined}
      right={<ReadChip read={read} />}
      answer={
        !waiting ? (
          <ReadNotice read={read} label="what waits for you" />
        ) : waiting.groups <= 0 ? (
          <>{describeWaiting(waiting).headline}.</>
        ) : (
          <>
            <strong className="font-semibold text-seam">{describeWaiting(waiting).headline}</strong>.
            {age ? <> The oldest has waited {age}.</> : null} Start with the first one below.
          </>
        )
      }
    />
  );
}
