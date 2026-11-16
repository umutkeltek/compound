"use client";
import * as Dialog from "@radix-ui/react-dialog";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useState, type ReactNode } from "react";
import {
  BarChartIcon,
  BoardIcon,
  BrainIcon,
  HomeIcon,
  SearchIcon,
  SlidersIcon,
  UsersIcon,
} from "@/components/kit/icons";
import { NAV_SECTIONS, sectionHref, sectionIsActive, type NavIconKey, type NavSection } from "@/lib/experience";
import { TERMS } from "@/lib/plain-words";

/**
 * The workspace sidebar: search, then the six sections named after what the
 * operator asks (lib/experience.ts), with his apps listed under Apps. Each
 * section's pages are tabs under the page header, so the rail stays short.
 * One count only: what waits for him, on Today. Unknown shows no count, never 0.
 */
export type NavProps = {
  /** Decisions waiting for you (lib/attention.ts waitingOnYou). The only count in the sidebar. null = not read yet. */
  decideCount?: number | null;
  /** @deprecated The Work count left the sidebar; accepted so existing callers still compile. */
  workCount?: number | null;
  variant?: "desktop" | "mobile";
  /** Server-rendered Apps list (never waits for compound). */
  apps?: ReactNode;
  /** Server-rendered system status card (autonomy, data freshness, instance). */
  status?: ReactNode;
};

const ICONS: Record<NavIconKey, (p: { size?: number }) => ReactNode> = {
  home: HomeIcon,
  chart: BarChartIcon,
  board: BoardIcon,
  users: UsersIcon,
  brain: BrainIcon,
  sliders: SlidersIcon,
};

/** The Today count: decisions waiting for you, named as the pages name them. */
export function waitingLabel(count: number): string {
  const noun = count === 1 ? TERMS.decision.name : TERMS.decision.plural;
  return `${count} ${noun} ${count === 1 ? "waits" : "wait"} for you`;
}

function WaitingPill({ count }: { count: number }) {
  if (count <= 0) return null;
  const label = waitingLabel(count);
  return (
    <span
      title={label}
      aria-label={label}
      data-nav-count="waiting"
      className="ml-auto inline-flex min-w-5 items-center justify-center rounded-pill bg-seam-soft px-1.5 text-xs font-medium tabular-nums text-seam"
    >
      {count}
    </span>
  );
}

/** Which console this is. Not a switcher and not a link: there is one workspace. */
function Workspace() {
  return (
    <div className="flex items-center gap-2.5 px-2.5 py-1" data-workspace>
      <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-strong text-md font-bold text-panel" aria-hidden>c</span>
      <span className="truncate text-md font-semibold text-ink">Compound</span>
    </div>
  );
}

function SearchButton() {
  return (
    <button
      type="button"
      onClick={() => window.dispatchEvent(new CustomEvent("compound:command-k"))}
      className="flex w-full items-center gap-2 rounded-xl border border-hair bg-panel px-3 py-2 text-left text-sm text-faint transition-colors hover:border-faint hover:text-muted"
      title="Find a page, an app, a piece of work or a decision (⌘K)"
    >
      <SearchIcon size={14} />
      <span className="flex-1">Search</span>
      <kbd className="rounded-md border border-hair bg-soft px-1.5 text-2xs font-medium text-faint">⌘K</kbd>
    </button>
  );
}

function SectionRow({
  section,
  active,
  badge,
  onNavigate,
}: {
  section: NavSection;
  active: boolean;
  badge?: ReactNode;
  onNavigate?: () => void;
}) {
  const Icon = ICONS[section.icon];
  return (
    <Link
      href={sectionHref(section)}
      className="nav-item"
      data-active={active}
      data-nav-section={section.id}
      aria-current={active ? "page" : undefined}
      onClick={onNavigate}
      title={section.question}
    >
      <span className={active ? "text-ink" : "text-faint"}><Icon size={15} /></span>
      <span className="truncate">{section.label}</span>
      {badge}
    </Link>
  );
}

function NavBody({
  pathname,
  decideCount,
  apps,
  status,
  onNavigate,
}: {
  pathname: string;
  decideCount?: number | null;
  apps?: ReactNode;
  status?: ReactNode;
  onNavigate?: () => void;
}) {
  return (
    <>
      <div className="flex flex-col gap-3">
        <Workspace />
        <SearchButton />
      </div>
      <nav className="flex flex-1 flex-col gap-0.5" aria-label="Primary">
        {NAV_SECTIONS.map((section) => (
          <div key={section.id} className="flex flex-col gap-0.5">
            <SectionRow
              section={section}
              active={sectionIsActive(pathname, section)}
              badge={section.id === "today" && decideCount != null ? <WaitingPill count={decideCount} /> : null}
              onNavigate={onNavigate}
            />
            {section.id === "apps" && apps ? <div className="mb-2 ml-3 border-l border-hair pl-1.5">{apps}</div> : null}
          </div>
        ))}
      </nav>
      <div className="flex flex-col gap-3">
        {status}
      </div>
    </>
  );
}

export function Nav({ decideCount, variant = "desktop", apps, status }: NavProps) {
  const pathname = usePathname();
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
          <Dialog.Content className="fixed inset-y-0 left-0 z-[60] flex w-[min(86vw,300px)] flex-col gap-5 overflow-y-auto overscroll-contain border-r border-hair bg-page px-3 py-4 shadow-lg focus:outline-none md:hidden">
            <div className="flex items-center justify-end">
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
            <Dialog.Title className="sr-only">Navigation</Dialog.Title>
            <Dialog.Description className="sr-only">Navigate between Compound console pages.</Dialog.Description>
            <NavBody pathname={pathname} decideCount={decideCount} apps={apps} status={status} onNavigate={() => setOpen(false)} />
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
    );
  }

  return (
    <aside className="sticky top-0 hidden h-screen w-[var(--nav-w)] shrink-0 flex-col gap-5 overflow-y-auto px-3 py-4 md:flex">
      <NavBody pathname={pathname} decideCount={decideCount} apps={apps} status={status} />
    </aside>
  );
}
