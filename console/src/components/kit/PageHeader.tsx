import Link from "next/link";
import { NAV_GROUPS } from "@/lib/experience";
import type { ReactNode } from "react";

/**
 * The one page header (DESIGN.md §3): the page's sidebar label as the H1,
 * a small kicker only when the H1 is a question or an entity name, one plain
 * sentence saying why the page exists (`lead`), the page's live answer when
 * the page asks a question (`answer`), an optional stat strip, and a quiet
 * right slot for read age / one action. The visible breadcrumb in the panel
 * header comes from the same sidebar registry (lib/experience.ts), so the
 * names a person sees — sidebar, breadcrumb, title — cannot drift.
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
  /** The sidebar label. Rendered small above the title when the title differs. */
  name?: string;
  /** The H1. Defaults to the sidebar label; question pages pass their question. */
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
  const page = NAV_GROUPS.flatMap((group) => group.items).find((item) => item.label === name);
  const next = action === undefined ? page?.nextAction : action;
  const description = lead ?? (answer ? null : page?.description);
  const heading = title ?? name;
  const kicker = !meta && name && title && title !== name ? name : null;
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
