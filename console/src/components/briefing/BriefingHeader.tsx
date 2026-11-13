import { ReadChip, ReadNotice, type ReadView } from "@/components/kit";
import type { WaitingOnYou } from "@/lib/attention";
import { humanAge } from "@/lib/briefing";
import { str } from "@/lib/compound";
import { unit } from "@/lib/counts";

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

/** "125d" → "125 days", "3h" → "3 hours", "now" → "a moment". */
function ageWords(value: unknown): string {
  const age = humanAge(value);
  const match = age.match(/^(\d+)([mhd])$/);
  if (!match) return age === "now" ? "a moment" : "";
  const n = Number(match[1]);
  const word = match[2] === "m" ? "minute" : match[2] === "h" ? "hour" : "day";
  return unit(n, word);
}

/**
 * Today's question and its answer, first on the page: "What needs you
 * today?" and one sentence built from the ONE "waiting for you" definition
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
  const age = ageWords(oldest);
  return (
    <header className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2 text-sm text-faint">
        <span>
          {greeting(now)} · {dateLabel(now)}
        </span>
        <ReadChip read={read} />
      </div>
      <h1 className="text-3xl font-semibold tracking-tight text-strong">What needs you today?</h1>
      {!waiting ? (
        <ReadNotice read={read} label="what waits for you" />
      ) : waiting.groups <= 0 ? (
        <p className="max-w-read text-lg leading-snug text-ink">
          Nothing needs you today. No decision is waiting for you.
        </p>
      ) : (
        <p className="max-w-read text-lg leading-snug text-ink">
          <strong className="font-semibold text-seam">{unit(waiting.groups, "decision")}</strong>{" "}
          {waiting.groups === 1 ? "waits" : "wait"} for you, with {unit(waiting.items, "item")} in all.
          {age ? <> The oldest has waited {age}.</> : null} Start with the first one below.
        </p>
      )}
    </header>
  );
}
