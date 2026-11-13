import { FreshnessChip } from "@/components/kit/FreshnessChip";

/**
 * The three honest states of a non-blocking read (lib/compound.ts
 * `readProjection`), as one small kit:
 *  - loading: no copy yet: a quiet placeholder that says the data is being
 *    read for the first time and roughly how long that takes;
 *  - loaded: the value, with a plain age and "updating…" while a refresh runs;
 *  - failed: "Could not read this. Last good copy is from <time>." with the
 *    last good value when there is one.
 * A read that has not landed never renders as zero, empty or "clean".
 *
 * Client-safe on purpose (no value import from `@/lib/compound`, which pulls
 * node builtins): `ReadView` is the structural slice of `LiveRead` it needs.
 */
export type ReadView = {
  status: "loading" | "live" | "stale" | "unreachable";
  generatedAt: string | null;
  lastGoodAt: string | null;
  error: string | null;
  failedAt: string | null;
  updating: boolean;
  outOfDate: boolean;
  warn: boolean;
  ageMs: number | null;
  lastRunMs: number | null;
  source: string;
};

/** A copy exists (fresh or not). */
export function hasCopy(read: Pick<ReadView, "status">): boolean {
  return read.status === "live" || read.status === "stale";
}

/** Anything on the page still loading or refreshing. */
export function readsPending(...reads: Array<Pick<ReadView, "status" | "updating"> | null | undefined>): boolean {
  return reads.some((read) => !!read && (read.status === "loading" || read.updating));
}

/** "about 40 seconds", "about 2 minutes". */
export function aboutDuration(ms: number): string {
  const seconds = Math.max(1, Math.round(ms / 1000));
  if (seconds < 60) return `about ${seconds} seconds`;
  const minutes = Math.round(seconds / 60);
  return minutes <= 1 ? "about a minute" : `about ${minutes} minutes`;
}

/** "just now", "3 min ago", "2 h ago", "3 days ago". */
export function plainAge(ms: number): string {
  if (ms < 60_000) return "just now";
  const minutes = Math.floor(ms / 60_000);
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return `${hours} h ago`;
  return `${Math.floor(hours / 24)} days ago`;
}

function clockTime(iso: string | null): string {
  if (!iso) return "";
  const t = new Date(iso);
  if (Number.isNaN(t.getTime())) return "";
  return t.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

function firstLine(text: string | null): string {
  const line = (text ?? "").split("\n").find((part) => part.trim()) ?? "";
  return line.length > 140 ? `${line.slice(0, 139)}…` : line;
}

/** Plain age chip for a read: "updated 3 min ago · updating…". */
export function ReadChip({ read }: { read: ReadView }) {
  if (read.status === "loading") {
    return (
      <span title={`source: ${read.source}`} className="mono inline-flex items-center gap-1.5 rounded-pill border border-hair bg-soft px-2.5 py-0.5 text-2xs font-semibold text-faint">
        <span className="h-1.5 w-1.5 rounded-full bg-faint" aria-hidden />
        reading…
      </span>
    );
  }
  return (
    <FreshnessChip
      generatedAt={read.generatedAt ?? read.lastGoodAt ?? undefined}
      source={read.source}
      updating={read.updating}
      failed={Boolean(read.error)}
    />
  );
}

/**
 * The placeholder for a read with no copy: first read running, or failed with
 * nothing to show. Never an empty state.
 */
export function ReadNotice({ read, label, className = "" }: { read: ReadView; label: string; className?: string }) {
  if (read.status === "unreachable") {
    return (
      <div className={`rounded-lg border border-seam-line bg-seam-soft px-4 py-4 text-sm text-seam ${className}`} data-read-state="failed">
        <p className="leading-relaxed">
          Could not read {label}. There is no good copy yet{read.updating ? "; trying again now." : "; it is retried in the background."}
        </p>
        <p className="mono mt-1 text-2xs" title={read.source}>
          {read.source}{read.error ? ` · ${firstLine(read.error)}` : ""}
        </p>
      </div>
    );
  }
  const how = read.lastRunMs ? `Last time it took ${aboutDuration(read.lastRunMs)}.` : "This can take a minute or two.";
  return (
    <div className={`rounded-lg border border-dashed border-hair bg-page px-4 py-4 text-sm text-muted ${className}`} data-read-state="loading">
      <p className="leading-relaxed">
        Reading {label} for the first time. {how}
      </p>
      <p className="mono mt-1 text-2xs text-faint" title={read.source}>{read.source}</p>
    </div>
  );
}

/** Shown above a last-good value when the latest refresh failed. */
export function ReadFailedBanner({ read, label }: { read: ReadView; label: string }) {
  if (!read.error || !hasCopy(read)) return null;
  const at = clockTime(read.generatedAt ?? read.lastGoodAt);
  const age = read.ageMs !== null ? ` (${plainAge(read.ageMs)})` : "";
  return (
    <div className="rounded-lg border border-seam-line bg-seam-soft px-4 py-2.5 text-sm text-seam" data-read-state="failed">
      Could not read {label}. Last good copy is from {at || "earlier"}{age}.
      <span className="mono ml-2 text-2xs" title={read.error}>{firstLine(read.error)}</span>
    </div>
  );
}

/** Render children only when a copy exists; otherwise the honest notice. */
export function ReadGate({
  read,
  label,
  children,
  className,
}: {
  read: ReadView;
  label: string;
  children: React.ReactNode;
  className?: string;
}) {
  if (!hasCopy(read)) return <ReadNotice read={read} label={label} className={className} />;
  return (
    <>
      <ReadFailedBanner read={read} label={label} />
      {children}
    </>
  );
}
