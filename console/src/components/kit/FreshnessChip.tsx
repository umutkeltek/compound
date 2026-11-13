/**
 * Every card declares provenance (DESIGN.md §3/§7.8): dot + mono label,
 * `title` = source command. The label is always the plain age of the copy
 * ("updated 3 min ago"), plus "updating…" while a refresh runs. The warning
 * colour appears only when the copy is older than `staleAfterMs` (10 minutes
 * by default) or the last refresh failed: heavy reads take one to two
 * minutes, so a 60-second "stale" rule made every label permanently amber.
 *
 * Client-safe: no import from `@/lib/compound` (node builtins), so the kit
 * barrel stays usable from client components like the Trace drawer.
 */
export type FreshnessStatus = "fresh" | "stale" | "unknown";

const DEFAULT_WARN_AFTER_MS = 10 * 60_000;

function ageMs(generatedAt: string | undefined): number | null {
  if (!generatedAt) return null;
  const t = new Date(generatedAt).getTime();
  if (Number.isNaN(t)) return null;
  return Math.max(0, Date.now() - t);
}

/** "just now", "3 min ago", "2 h ago", "3 days ago". */
function plainAge(ms: number): string {
  if (ms < 60_000) return "just now";
  const minutes = Math.floor(ms / 60_000);
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return `${hours} h ago`;
  return `${Math.floor(hours / 24)} days ago`;
}

const DOT_TONE: Record<FreshnessStatus, string> = {
  fresh: "bg-good",
  stale: "bg-seam",
  unknown: "bg-faint",
};

const TEXT_TONE: Record<FreshnessStatus, string> = {
  fresh: "text-muted",
  stale: "text-seam",
  unknown: "text-faint",
};

export function FreshnessChip({
  generatedAt,
  source,
  staleAfterMs = DEFAULT_WARN_AFTER_MS,
  updating = false,
  failed = false,
}: {
  generatedAt?: string;
  source?: string;
  staleAfterMs?: number;
  /** a refresh is running now. */
  updating?: boolean;
  /** the latest refresh failed (the copy shown is the last good one). */
  failed?: boolean;
}) {
  const age = ageMs(generatedAt);
  const status: FreshnessStatus = age === null ? "unknown" : failed || age > staleAfterMs ? "stale" : "fresh";
  const parts = [age === null ? "age unknown" : `updated ${plainAge(age)}`];
  if (failed) parts.push("last refresh failed");
  if (updating) parts.push("updating…");

  return (
    <span
      title={source ? `source: ${source}` : undefined}
      data-freshness={status}
      className={`mono inline-flex items-center gap-1.5 rounded-pill border border-hair bg-soft px-2.5 py-0.5 text-2xs font-semibold tabular-nums ${TEXT_TONE[status]}`}
    >
      <span className={`h-1.5 w-1.5 rounded-full ${DOT_TONE[status]}`} aria-hidden />
      {parts.join(" · ")}
    </span>
  );
}
