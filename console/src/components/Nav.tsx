"use client";

import * as Dialog from "@radix-ui/react-dialog";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useState } from "react";

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
  variant?: "desktop" | "mobile";
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
    {
      // CONTROL (Part 2 of the redesign plan). Routing & cost lands here in
      // Phase 4; Settings lands alongside from a parallel lane (route exists at
      // cutover). Neither carries a nav badge yet — routing's needs-you count
      // would need a new NavContainer prop (out of this lane's narrow Nav scope).
      label: "Control",
      links: [
        { label: "Routing & cost", href: "/routing" },
        { label: "Settings", href: "/settings" },
      ],
    },
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

function Brand() {
  return (
    <div className="flex items-center gap-2.5 px-1">
      <span className="flex h-8 w-8 items-center justify-center rounded-md bg-ink text-md font-bold text-panel">
        c
      </span>
      <div className="leading-tight">
        <div className="text-base font-semibold text-ink">compound</div>
        <div className="text-xs text-faint">console</div>
      </div>
    </div>
  );
}

function NavLinks({
  groups,
  pathname,
  mobile = false,
  onNavigate,
}: {
  groups: Group[];
  pathname: string;
  mobile?: boolean;
  onNavigate?: () => void;
}) {
  const linkClass = mobile ? "nav-item py-2.5" : "nav-item";
  return (
    <nav className="flex flex-1 flex-col gap-5">
      <Link href="/" className={linkClass} data-active={pathname === "/"} aria-current={pathname === "/" ? "page" : undefined} onClick={onNavigate}>
        <Dot active={pathname === "/"} />
        Briefing
      </Link>

      {groups.map((group) => (
        <div key={group.label}>
          <div className="section-label mb-1.5 px-2">{group.label}</div>
          <div className="flex flex-col gap-0.5">
            {group.links.map((link) =>
              link.href ? (
                <Link
                  key={link.label}
                  href={link.href}
                  className={linkClass}
                  data-active={pathname === link.href}
                  aria-current={pathname === link.href ? "page" : undefined}
                  onClick={onNavigate}
                >
                  <Dot active={pathname === link.href} />
                  {link.label}
                  {link.badge ? <CountPill count={link.badge.count} tone={link.badge.tone} /> : null}
                </Link>
              ) : (
                <span
                  key={link.label}
                  className={linkClass}
                  data-soon="true"
                  title="Appends when its projection lands (base + append)"
                >
                  <Dot active={false} />
                  {link.label}
                  <span className="ml-auto text-2xs text-faint">soon</span>
                </span>
              ),
            )}
          </div>
        </div>
      ))}
    </nav>
  );
}

function NavFooter() {
  return (
    <div className="px-1 text-2xs leading-relaxed text-faint">
      Projection-first.
      <br />
      Continue uses governed local commands in operator mode.
    </div>
  );
}

export function Nav({ decideCount, workCount, variant = "desktop" }: NavProps) {
  const pathname = usePathname();
  const groups = groupsFor(decideCount, workCount);
  const [open, setOpen] = useState(false);

  if (variant === "mobile") {
    return (
      <Dialog.Root open={open} onOpenChange={setOpen}>
        <Dialog.Trigger asChild>
          <button
            type="button"
            aria-label="Open navigation"
            title="Open navigation"
            className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md border border-hair bg-panel text-muted hover:bg-soft md:hidden"
          >
            <span aria-hidden className="grid gap-1">
              <span className="block h-px w-4 bg-current" />
              <span className="block h-px w-4 bg-current" />
              <span className="block h-px w-4 bg-current" />
            </span>
          </button>
        </Dialog.Trigger>
        <Dialog.Portal>
          <Dialog.Overlay className="fixed inset-0 z-[60] bg-scrim backdrop-blur-sm motion-reduce:backdrop-blur-none md:hidden" />
          <Dialog.Content className="fixed inset-y-0 left-0 z-[60] flex w-[min(86vw,300px)] flex-col gap-6 overflow-y-auto overscroll-contain border-r border-hair bg-panel px-4 py-5 shadow-lg focus:outline-none md:hidden">
            <div className="flex items-center justify-between gap-3">
              <Dialog.Title asChild><div><Brand /></div></Dialog.Title>
              <Dialog.Close asChild>
                <button
                  type="button"
                  aria-label="Close navigation"
                  title="Close navigation"
                  className="inline-flex h-8 w-8 items-center justify-center rounded-md border border-hair text-lg text-muted hover:bg-soft"
                >
                  <span aria-hidden>×</span>
                </button>
              </Dialog.Close>
            </div>
            <Dialog.Description className="sr-only">Navigate between Compound console workspaces.</Dialog.Description>
            <NavLinks groups={groups} pathname={pathname} mobile onNavigate={() => setOpen(false)} />
            <NavFooter />
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
    );
  }

  return (
    <aside className="sticky top-0 hidden h-screen w-[var(--nav-w)] shrink-0 flex-col gap-6 border-r border-hair bg-panel/70 px-4 py-5 backdrop-blur md:flex">
      <Brand />
      <NavLinks groups={groups} pathname={pathname} />
      <NavFooter />
    </aside>
  );
}
