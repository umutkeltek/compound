"use client";
import * as Dialog from "@radix-ui/react-dialog";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useState, type ReactNode } from "react";
import {
  BarChartIcon,
  BoardIcon,
  BrainIcon,
  ChevronDownIcon,
  ChevronRightIcon,
  ChevronsUpDownIcon,
  CoinsIcon,
  GavelIcon,
  GridIcon,
  HomeIcon,
  LoopIcon,
  PulseIcon,
  RouteIcon,
  SearchIcon,
  ShieldIcon,
  SlidersIcon,
  SourceIcon,
  TargetIcon,
  UsersIcon,
  LayersIcon,
} from "@/components/kit/icons";
import { NAV_GROUPS, routeIsActive, type NavGroup, type NavIconKey } from "@/lib/experience";

/**
 * The workspace sidebar. Layout follows the operator's reference: workspace
 * switcher, search, an always-open Essentials group, the Apps list (projects),
 * Growth, then Knowledge and System, which collapse until one of their pages is
 * active. Every console route stays reachable from here — nothing is hidden,
 * only quieted.
 */
type BadgeTone = "seam" | "quiet";

export type NavProps = {
  /** Decision groups waiting on you (lib/attention.ts waitingOnYou). Amber — the only amber badge. null = not read yet. */
  decideCount?: number | null;
  /** Work items waiting on you (same function). Quiet slate. null = not read yet. */
  workCount?: number | null;
  variant?: "desktop" | "mobile";
  /** Server-rendered Apps list (never waits for compound). */
  apps?: ReactNode;
  /** Server-rendered system status card (autonomy, data freshness, instance). */
  status?: ReactNode;
};

const ICONS: Record<NavIconKey, (p: { size?: number }) => ReactNode> = {
  home: HomeIcon,
  board: BoardIcon,
  gavel: GavelIcon,
  pulse: PulseIcon,
  users: UsersIcon,
  route: RouteIcon,
  chart: BarChartIcon,
  target: TargetIcon,
  loop: LoopIcon,
  coins: CoinsIcon,
  brain: BrainIcon,
  law: ShieldIcon,
  source: SourceIcon,
  map: LayersIcon,
  sliders: SlidersIcon,
  grid: GridIcon,
};

function CountPill({ count, tone }: { count: number; tone: BadgeTone }) {
  if (count <= 0) return null;
  const cls = tone === "seam" ? "bg-seam-soft text-seam" : "text-faint";
  // Name the unit: the two badges count different things (lib/attention.ts waitingOnYou).
  const unit = tone === "seam" ? (count === 1 ? "decision group waits" : "decision groups wait") : count === 1 ? "work item waits" : "work items wait";
  return (
    <span
      title={`${count} ${unit} on you`}
      className={`ml-auto inline-flex min-w-5 items-center justify-center rounded-pill px-1.5 text-xs font-medium tabular-nums ${cls}`}
    >
      {count}
    </span>
  );
}

function Workspace() {
  return (
    <Link
      href="/settings"
      className="flex items-center gap-2.5 rounded-xl border border-hair bg-panel px-2.5 py-2 shadow-sm transition-colors hover:bg-soft"
      title="Workspace settings"
    >
      <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-strong text-md font-bold text-panel">c</span>
      <span className="min-w-0 flex-1 leading-tight">
        <span className="block text-2xs text-faint">Workspace</span>
        <span className="block truncate text-md font-semibold text-ink">Compound</span>
      </span>
      <span className="text-faint"><ChevronsUpDownIcon size={14} /></span>
    </Link>
  );
}

function SearchButton() {
  return (
    <button
      type="button"
      onClick={() => window.dispatchEvent(new CustomEvent("compound:command-k"))}
      className="flex w-full items-center gap-2 rounded-xl border border-hair bg-panel px-3 py-2 text-left text-sm text-faint transition-colors hover:border-faint hover:text-muted"
      title="Search pages and commands (⌘K)"
    >
      <SearchIcon size={14} />
      <span className="flex-1">Search</span>
      <kbd className="rounded-md border border-hair bg-soft px-1.5 text-2xs font-medium text-faint">⌘K</kbd>
    </button>
  );
}

function GroupLabel({ children, action }: { children: ReactNode; action?: ReactNode }) {
  return (
    <div className="flex items-center justify-between px-2.5 pb-1.5 pt-1">
      <span className="text-xs font-medium text-faint">{children}</span>
      {action}
    </div>
  );
}

function NavRow({
  href,
  label,
  icon,
  active,
  title,
  badge,
  onNavigate,
}: {
  href: string;
  label: string;
  icon: NavIconKey;
  active: boolean;
  title?: string;
  badge?: ReactNode;
  onNavigate?: () => void;
}) {
  const Icon = ICONS[icon];
  return (
    <Link href={href} className="nav-item" data-active={active} aria-current={active ? "page" : undefined} onClick={onNavigate} title={title}>
      <span className={active ? "text-ink" : "text-faint"}><Icon size={15} /></span>
      <span className="truncate">{label}</span>
      {badge}
    </Link>
  );
}

function Group({
  group,
  pathname,
  decideCount,
  workCount,
  onNavigate,
}: {
  group: NavGroup;
  pathname: string;
  decideCount?: number | null;
  workCount?: number | null;
  onNavigate?: () => void;
}) {
  const containsActive = group.items.some((item) => routeIsActive(pathname, item));
  const [open, setOpen] = useState(!group.collapsible || containsActive);
  useEffect(() => {
    if (containsActive) setOpen(true);
  }, [containsActive]);

  const rows = group.items.map((item) => {
    const badge =
      item.badge === "decide" && decideCount != null ? <CountPill count={decideCount} tone="seam" />
        : item.badge === "work" && workCount != null ? <CountPill count={workCount} tone="quiet" />
          : null;
    return (
      <NavRow
        key={item.href}
        href={item.href}
        label={item.label}
        icon={item.icon}
        active={routeIsActive(pathname, item)}
        title={item.description}
        badge={badge}
        onNavigate={onNavigate}
      />
    );
  });

  if (!group.collapsible) {
    return (
      <div>
        <GroupLabel>{group.label}</GroupLabel>
        <div className="flex flex-col gap-0.5">{rows}</div>
      </div>
    );
  }
  return (
    <div>
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className="flex w-full items-center justify-between rounded-lg px-2.5 pb-1.5 pt-1 text-xs font-medium text-faint hover:text-muted"
      >
        <span>{group.label}</span>
        <span className="flex items-center gap-1.5">
          {!open ? <span className="tabular-nums">{group.items.length}</span> : null}
          {open ? <ChevronDownIcon size={13} /> : <ChevronRightIcon size={13} />}
        </span>
      </button>
      {open ? <div className="flex flex-col gap-0.5">{rows}</div> : null}
    </div>
  );
}

function Divider() {
  return <div className="mx-2.5 border-t border-hair" />;
}

function NavBody({
  pathname,
  decideCount,
  workCount,
  apps,
  status,
  onNavigate,
}: {
  pathname: string;
  decideCount?: number | null;
  workCount?: number | null;
  apps?: ReactNode;
  status?: ReactNode;
  onNavigate?: () => void;
}) {
  const [essentials, growth, ...rest] = NAV_GROUPS;
  return (
    <>
      <div className="flex flex-col gap-3">
        <Workspace />
        <SearchButton />
      </div>
      <nav className="flex flex-1 flex-col gap-4" aria-label="Primary">
        <Group group={essentials} pathname={pathname} decideCount={decideCount} workCount={workCount} onNavigate={onNavigate} />
        <Divider />
        {apps}
        <Group group={growth} pathname={pathname} onNavigate={onNavigate} />
        <Divider />
        {rest.map((group) => (
          <Group key={group.id} group={group} pathname={pathname} onNavigate={onNavigate} />
        ))}
      </nav>
      <div className="flex flex-col gap-3">
        {status}
        <Divider />
      </div>
    </>
  );
}

export function Nav({ decideCount, workCount, variant = "desktop", apps, status }: NavProps) {
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
            <NavBody pathname={pathname} decideCount={decideCount} workCount={workCount} apps={apps} status={status} onNavigate={() => setOpen(false)} />
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
    );
  }

  return (
    <aside className="sticky top-0 hidden h-screen w-[var(--nav-w)] shrink-0 flex-col gap-5 overflow-y-auto px-3 py-4 md:flex">
      <NavBody pathname={pathname} decideCount={decideCount} workCount={workCount} apps={apps} status={status} />
    </aside>
  );
}
