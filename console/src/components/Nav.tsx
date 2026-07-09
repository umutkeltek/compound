"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

/**
 * Groups restructured per the redesign plan (Part 2): Briefing stands alone
 * at the top; OPERATE / KNOW hold the working surfaces; CONTROL (Settings,
 * Routing & cost) is intentionally omitted below — no routes exist yet.
 */
type BadgeTone = "seam" | "quiet";
type NavBadge = { count: number; tone: BadgeTone };
type NavLink = { label: string; href?: string; soon?: boolean; badge?: NavBadge };
type Group = { label: string; links: NavLink[] };

export type NavProps = {
  /** Needs-you group count for the Decide queue (interim source: `compound reviews --json` groups — see NavContainer). Amber, the only amber badge. */
  decideCount?: number | null;
  /** Cheap needs-claim count from the existing workbench fetch (`counts.needs_claim`). Quiet slate — not a needs-you signal. */
  workCount?: number | null;
};

function groupsFor(decideCount: number | null | undefined, workCount: number | null | undefined): Group[] {
  return [
    {
      label: "Operate",
      links: [
        {
          label: "Decide",
          href: "/reviews", // becomes the unified Decide queue in Phase 2; route renames then too
          badge: decideCount != null ? { count: decideCount, tone: "seam" } : undefined,
        },
        {
          label: "Work",
          href: "/work",
          badge: workCount != null ? { count: workCount, tone: "quiet" } : undefined,
        },
        { label: "Flow", href: "/flow" }, // stays a route until Phase 4 (Work's Pipeline lens absorbs it)
        { label: "Agents", href: "/executors" },
        { label: "Loops", href: "/loops" },
      ],
    },
    {
      label: "Know",
      links: [
        { label: "Memory", href: "/memory" },
        { label: "Capabilities", href: "/map" }, // route renames in Phase 4
        { label: "Sources", href: "/sources" },
      ],
    },
    // CONTROL (Settings, Routing & cost) intentionally omitted — no routes
    // land in Phase 1 (Part 2 of the redesign plan lists both under Settings'
    // 7 groups, Phase 4). Add a group here once those pages exist.
  ];
}

function Dot({ active }: { active: boolean }) {
  return (
    <span
      className="h-1.5 w-1.5 rounded-full"
      style={{ background: active ? "var(--work-fg)" : "var(--hair-2)" }}
      aria-hidden
    />
  );
}

function CountPill({ count, tone }: { count: number; tone: BadgeTone }) {
  if (count <= 0) return null;
  const cls = tone === "seam" ? "border-seam-line bg-seam-soft text-seam" : "border-hair bg-soft-2 text-muted";
  return (
    <span
      className={`mono ml-auto inline-flex min-w-5 items-center justify-center rounded-pill border px-1.5 py-0.5 text-2xs font-semibold tabular-nums ${cls}`}
    >
      {count}
    </span>
  );
}

export function Nav({ decideCount, workCount }: NavProps) {
  const pathname = usePathname();
  const groups = groupsFor(decideCount, workCount);

  return (
    <aside className="sticky top-0 flex h-screen w-[var(--nav-w)] shrink-0 flex-col gap-6 border-r border-hair bg-panel/70 px-4 py-5 backdrop-blur">
      <div className="flex items-center gap-2.5 px-1">
        <span className="flex h-8 w-8 items-center justify-center rounded-md bg-ink text-md font-bold text-panel">
          c
        </span>
        <div className="leading-tight">
          <div className="text-base font-semibold text-ink">compound</div>
          <div className="text-xs text-faint">console</div>
        </div>
      </div>

      <nav className="flex flex-1 flex-col gap-5">
        {/* Briefing — top, ungrouped (Part 2 of the redesign plan). */}
        <Link href="/" className="nav-item" data-active={pathname === "/"}>
          <Dot active={pathname === "/"} />
          Briefing
        </Link>

        {groups.map((g) => (
          <div key={g.label}>
            <div className="section-label mb-1.5 px-2">{g.label}</div>
            <div className="flex flex-col gap-0.5">
              {g.links.map((l) =>
                l.href ? (
                  <Link key={l.label} href={l.href} className="nav-item" data-active={pathname === l.href}>
                    <Dot active={pathname === l.href} />
                    {l.label}
                    {l.badge ? <CountPill count={l.badge.count} tone={l.badge.tone} /> : null}
                  </Link>
                ) : (
                  <span
                    key={l.label}
                    className="nav-item"
                    data-soon="true"
                    title="Appends when its projection lands (base + append)"
                  >
                    <Dot active={false} />
                    {l.label}
                    <span className="ml-auto text-2xs text-faint">soon</span>
                  </span>
                ),
              )}
            </div>
          </div>
        ))}
      </nav>

      <div className="px-1 text-2xs leading-relaxed text-faint">
        Projection-first.
        <br />
        Continue uses governed local commands in operator mode.
      </div>
    </aside>
  );
}
