import type { ReadView } from "@/components/kit";
import type { WaitingOnYou } from "@/lib/attention";
import { humanAge, isOld } from "@/lib/briefing";
import { str } from "@/lib/compound";

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

const PILL = "inline-flex w-fit items-center gap-2 whitespace-nowrap rounded-pill border px-3 py-1 text-xs font-semibold tabular-nums";

/**
 * "N decision groups wait on you · M items · oldest age", from the ONE
 * definition (lib/attention.ts `waitingOnYou`) the sidebar and Work use, with
 * the units named. Amber (seam) whenever something waits (DESIGN.md §0/§2).
 * An attention read that has not landed is never shown as zero or "clear".
 */
function AttentionPill({ waiting, read, oldest }: { waiting: WaitingOnYou | null; read: ReadView; oldest: unknown }) {
  if (!waiting) {
    const failed = read.status === "unreachable";
    return (
      <span className={`${PILL} ${failed ? "border-seam-line bg-seam-soft text-seam" : "border-hair bg-soft text-faint"}`}>
        <span className={`h-1.5 w-1.5 rounded-full ${failed ? "bg-seam" : "bg-faint"}`} aria-hidden />
        {failed ? "Could not read what waits on you" : "Reading what waits on you…"}
      </span>
    );
  }
  const none = waiting.groups <= 0;
  const warn = isOld(oldest);
  const age = humanAge(oldest);
  const tone = none ? "border-hair bg-soft text-muted" : "border-seam-line bg-seam-soft text-seam";
  return (
    <span className={`${PILL} ${tone}`} title="Decision groups (one card each on Decide) and the items inside them">
      <span className={`h-1.5 w-1.5 rounded-full ${none ? "bg-faint" : "bg-seam"}`} aria-hidden />
      {none ? (
        "No decisions wait on you"
      ) : (
        <>
          {waiting.groups.toLocaleString("en-US")} decision {waiting.groups === 1 ? "group waits" : "groups wait"} on you
          <span className="opacity-70">· {waiting.items.toLocaleString("en-US")} {waiting.items === 1 ? "item" : "items"}</span>
          {age ? <span className={warn ? "" : "opacity-70"}>· oldest {age}</span> : null}
        </>
      )}
    </span>
  );
}

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
  return (
    <header className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight text-strong">{greeting(now)}</h1>
        <div className="mt-1 text-sm text-faint">{dateLabel(now)}</div>
      </div>
      <AttentionPill waiting={waiting} read={read} oldest={oldest} />
    </header>
  );
}
