import Link from "next/link";

/**
 * Knows strip (redesign plan Part 3 — kept, restyled): what the system believes
 * it can act on, what new evidence reopened, what's parked for the operator's
 * call. Memory domain (violet) accent only; big tabular values (DESIGN.md §3).
 */
export function KnowsStrip({
  promoted,
  parked,
  reopened,
  promotedThisWeek,
}: {
  promoted: number;
  parked: number;
  reopened: number;
  promotedThisWeek: number;
}) {
  return (
    <div className="grid grid-cols-2 gap-6 lg:grid-cols-4">
      <div>
        <div className="text-2xl font-extrabold leading-none tracking-tight tabular-nums text-ink">
          {promoted.toLocaleString("en-US")}
          {promotedThisWeek > 0 ? <span className="ml-1.5 text-sm font-semibold text-good">+{promotedThisWeek}</span> : null}
        </div>
        <div className="mt-1.5 flex items-center gap-1.5 text-sm text-muted">
          <span className="h-1.5 w-1.5 rounded-full bg-memory" aria-hidden />
          beliefs it can act on
        </div>
      </div>
      <div>
        <div className="text-2xl font-extrabold leading-none tracking-tight tabular-nums text-ink">{reopened.toLocaleString("en-US")}</div>
        <div className="mt-1.5 text-sm text-muted">reopened by new evidence</div>
      </div>
      <div>
        <div className="text-2xl font-extrabold leading-none tracking-tight tabular-nums text-ink">{parked.toLocaleString("en-US")}</div>
        <div className="mt-1.5 text-sm text-muted">parked for your call</div>
      </div>
      <div>
        <Link
          href="/memory"
          className="text-md font-semibold text-work hover:underline focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-focus"
        >
          Ask what it knows →
        </Link>
        <div className="mt-1.5 text-sm text-muted">promoted beliefs, with sources</div>
      </div>
    </div>
  );
}
