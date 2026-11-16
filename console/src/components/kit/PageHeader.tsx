import Link from "next/link";
import { NavTabs } from "@/components/NavTabs";
import { placeForName } from "@/lib/experience";
import type { ReactNode } from "react";

/**
 * The one page header (DESIGN.md §3): the page's name from the registry
 * (lib/experience.ts) as the H1, the section's name as a small kicker when
 * the H1 is an item (an app, a work item) or a question, one plain sentence
 * saying why the page exists (`lead`), the page's live answer when the page
 * asks a question (`answer`), an optional stat strip, a quiet right slot for
 * read age, and last the row of the section's pages as tabs. `name` may be the
 * page's current label or an earlier one ("Decide", "Portfolio", "Live"), so
 * callers keep working while the names a person sees (sidebar, tabs,
 * breadcrumb, title) come from one place and cannot drift.
 */
export function PageHeader({
  name,
  title,
  meta,
  lead,
  answer,
  stats,
  right,
  action,
}: {
  /** The page's name in lib/experience.ts (its label or an earlier name). Picks the default H1, lead, button and tabs. */
  name?: string;
  /** The H1. Defaults to the page's label; item and question pages pass their own. */
  title?: ReactNode;
  /** A quiet context line above the title; replaces the name kicker (Today's salutation). */
  meta?: ReactNode;
  /** One plain sentence: why this page exists. */
  lead?: ReactNode;
  /** The page's live answer, when the title is a question. */
  answer?: ReactNode;
  stats?: ReactNode;
  right?: ReactNode;
  action?: { label: string; href: string } | null;
}) {
  const place = placeForName(name);
  const page = place?.page;
  const next = action === undefined ? page?.nextAction : action;
  const description = lead ?? (answer ? null : page?.description);
  const label = page?.label ?? name;
  const heading = title ?? label;
  const kicker = !meta && place && title && title !== label ? place.section.label : null;
  return (
    <header className="flex flex-col gap-3">
      {meta ? <div className="text-sm text-faint">{meta}</div> : null}
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          {kicker ? <div className="text-sm font-semibold text-faint">{kicker}</div> : null}
          <h1 className="text-3xl font-semibold tracking-tight text-ink">{heading}</h1>
        </div>
        {next ? <Link href={next.href} className="inline-flex min-h-11 shrink-0 items-center justify-center gap-2 rounded-md border border-strong bg-strong px-4 py-2 text-sm font-semibold text-panel transition-colors hover:bg-ink focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-focus">{next.label}<span aria-hidden>→</span></Link> : null}
      </div>
      {description ? <div className="max-w-read text-sm leading-relaxed text-muted">{description}</div> : null}
      {answer ? <div className="max-w-read text-lg leading-snug text-ink">{answer}</div> : null}
      {right ? <div className="flex min-w-0 flex-wrap items-center gap-2 text-xs text-muted">{right}</div> : null}
      {stats ? <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm text-muted">{stats}</div> : null}
      <NavTabs fallback={place?.section.id} />
    </header>
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
