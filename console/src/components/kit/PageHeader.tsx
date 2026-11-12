import type { ReactNode } from "react";

/**
 * Page title block (DESIGN.md §3, refreshed to the operator's 2026-11-12
 * references): a large title, an optional one-line stat strip under it
 * ("26 open · 2 blocked"), and a right-side actions slot. The breadcrumb now
 * lives in the panel header for every page, so the `breadcrumb` prop is kept
 * for call-site compatibility and rendered only for screen readers.
 */
export function PageHeader({
  breadcrumb,
  title,
  stats,
  right,
}: {
  breadcrumb?: ReactNode;
  title: ReactNode;
  stats?: ReactNode;
  right?: ReactNode;
}) {
  return (
    <div className="flex flex-wrap items-end justify-between gap-4">
      <div className="min-w-0">
        {breadcrumb ? <div className="sr-only">{breadcrumb}</div> : null}
        <h1 className="text-3xl font-semibold tracking-tight text-ink">{title}</h1>
        {stats ? <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-sm text-muted">{stats}</div> : null}
      </div>
      {right ? <div className="flex min-w-0 w-full flex-wrap items-center gap-2 sm:w-auto sm:justify-end">{right}</div> : null}
    </div>
  );
}

/** One stat in a PageHeader strip: bold number, quiet label. `tone` colours the whole stat (e.g. crit for overdue). */
export function HeaderStat({ value, label, tone }: { value: ReactNode; label: ReactNode; tone?: "crit" | "seam" | "good" }) {
  const color = tone === "crit" ? "text-crit" : tone === "seam" ? "text-seam" : tone === "good" ? "text-good" : "";
  return (
    <span className={color}>
      <span className={`font-semibold tabular-nums ${color || "text-ink"}`}>{value}</span> {label}
    </span>
  );
}

export function Toolbar({ children }: { children: ReactNode }) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-hair bg-panel px-3 py-2">
      {children}
    </div>
  );
}
