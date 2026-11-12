/**
 * Stable product routes grouped into user-facing surfaces, plus the sidebar
 * groups that render them. Keep this client-safe: the shell and command
 * palette both consume it.
 *
 * Every route stays reachable from the sidebar: Essentials and Growth are
 * always open; Knowledge and System collapse until one of their pages is
 * active, so advanced routes stay one click away without making the rail loud.
 */
export type ExperienceSurfaceId =
  | "today"
  | "live"
  | "work"
  | "decide"
  | "growth"
  | "knowledge"
  | "system";

export type ExperienceRoute = { label: string; href: string; match?: string[] };
export type ExperienceSurface = {
  id: ExperienceSurfaceId;
  label: string;
  href: string;
  description: string;
  routes: ExperienceRoute[];
};

export const EXPERIENCE_SURFACES: ExperienceSurface[] = [
  {
    id: "today",
    label: "Today",
    href: "/",
    description: "The few things worth your attention now.",
    routes: [{ label: "Briefing", href: "/" }],
  },
  {
    id: "work",
    label: "Work",
    href: "/work",
    description: "What is moving, what is blocked, and what can move next.",
    routes: [{ label: "Board", href: "/work", match: ["/work/", "/flow"] }],
  },
  {
    id: "decide",
    label: "Decide",
    href: "/reviews",
    description: "Consequential calls, with evidence and downstream impact.",
    routes: [{ label: "Decision queue", href: "/reviews" }],
  },
  {
    id: "live",
    label: "Live",
    href: "/live",
    description: "Harnesses, work, handoffs, and waits as they happen.",
    routes: [
      { label: "Overview", href: "/live" },
      { label: "Harnesses", href: "/executors" },
      { label: "Coordination", href: "/traffic" },
    ],
  },
  {
    id: "growth",
    label: "Growth",
    href: "/apps",
    description: "How each app is found, used, paid for and kept — and where the next hour should go.",
    routes: [
      { label: "Portfolio", href: "/apps", match: ["/apps/"] },
      { label: "Growth audit", href: "/growth" },
      { label: "Feedback loops", href: "/loops" },
      { label: "Ads", href: "/ads" },
    ],
  },
  {
    id: "knowledge",
    label: "Knowledge",
    href: "/memory",
    description: "What Compound knows, trusts, and is still evaluating.",
    routes: [
      { label: "Memory", href: "/memory", match: ["/map/memory"] },
      { label: "Standing law", href: "/standing-law" },
      { label: "Sources", href: "/sources" },
    ],
  },
  {
    id: "system",
    label: "System",
    href: "/map",
    description: "Capabilities, routes, policy, and effective authority.",
    routes: [
      { label: "Map", href: "/map", match: ["/map/"] },
      { label: "Routing", href: "/routing" },
      { label: "Settings", href: "/settings" },
      { label: "View grammar", href: "/representation" },
    ],
  },
];

/** Sidebar icon keys; Nav maps them to kit glyphs (keeps this file client-safe and JSX-free). */
export type NavIconKey =
  | "home" | "board" | "gavel" | "pulse" | "users" | "route"
  | "chart" | "target" | "loop" | "coins"
  | "brain" | "law" | "source"
  | "map" | "sliders" | "grid";

export type NavItem = {
  label: string;
  href: string;
  icon: NavIconKey;
  description: string;
  match?: string[];
  /** Which attention count this row shows, if any. */
  badge?: "decide" | "work";
};

export type NavGroup = {
  id: "essentials" | "growth" | "knowledge" | "system";
  label: string;
  collapsible: boolean;
  items: NavItem[];
};

export const NAV_GROUPS: NavGroup[] = [
  {
    id: "essentials",
    label: "Essentials",
    collapsible: false,
    items: [
      { label: "Today", href: "/", icon: "home", description: "The few things worth your attention now." },
      { label: "Work", href: "/work", icon: "board", match: ["/work/", "/flow"], badge: "work", description: "Every work item as a board, list, flow or timeline." },
      { label: "Decide", href: "/reviews", icon: "gavel", badge: "decide", description: "Calls only you can make, with evidence." },
      { label: "Live", href: "/live", icon: "pulse", description: "What is running right now." },
    ],
  },
  {
    id: "growth",
    label: "Growth",
    collapsible: false,
    items: [
      { label: "Portfolio", href: "/apps", icon: "chart", match: ["/apps/"], description: "Revenue, downloads, traffic and ads across every app." },
      { label: "Growth audit", href: "/growth", icon: "target", description: "25 growth areas per app: measured, stale or blind." },
      { label: "Feedback loops", href: "/loops", icon: "loop", description: "Change → signal → hypothesis → review, per app." },
      { label: "Ads", href: "/ads", icon: "coins", description: "Ad spend, installs and cost per install." },
    ],
  },
  {
    id: "knowledge",
    label: "Knowledge",
    collapsible: true,
    items: [
      { label: "Memory", href: "/memory", icon: "brain", match: ["/map/memory"], description: "What Compound knows and trusts." },
      { label: "Standing law", href: "/standing-law", icon: "law", description: "Rules agents follow without asking." },
      { label: "Sources", href: "/sources", icon: "source", description: "Where knowledge comes from." },
    ],
  },
  {
    id: "system",
    label: "System",
    collapsible: true,
    items: [
      { label: "Harnesses", href: "/executors", icon: "users", description: "Agents, lanes, sessions and cost." },
      { label: "Coordination", href: "/traffic", icon: "route", description: "Who is working on what, who waits on whom." },
      { label: "Map", href: "/map", icon: "map", match: ["/map/"], description: "Capabilities, routes and lineage." },
      { label: "Routing", href: "/routing", icon: "sliders", description: "Which model lane handles which work." },
      { label: "View grammar", href: "/representation", icon: "grid", description: "How the console chooses a view." },
    ],
  },
];

function matches(pathname: string, route: { href: string; match?: string[] }): boolean {
  if (route.href === "/") return pathname === "/";
  return pathname === route.href || (route.match ?? []).some((prefix) => pathname.startsWith(prefix));
}

export function surfaceForPath(pathname: string): ExperienceSurface {
  return EXPERIENCE_SURFACES.find((surface) => surface.routes.some((route) => matches(pathname, route))) ?? EXPERIENCE_SURFACES[0];
}

export function routeIsActive(pathname: string, route: { href: string; match?: string[] }): boolean {
  if (route.href === "/map" && pathname.startsWith("/map/memory")) return false;
  return matches(pathname, route);
}

/** The page title for the shell breadcrumb: the most specific sidebar row that matches. */
export function navItemForPath(pathname: string): NavItem | undefined {
  for (const group of NAV_GROUPS) {
    for (const item of group.items) {
      if (routeIsActive(pathname, item)) return item;
    }
  }
  if (pathname === "/settings") return { label: "Settings", href: "/settings", icon: "sliders", description: "Console and system settings." };
  return undefined;
}
