/**
 * Stable product routes grouped into user-facing surfaces, plus the sidebar
 * groups that render them. Keep this client-safe: the shell and command
 * palette both consume it.
 *
 * Every route stays reachable from the sidebar: Essentials and Growth are
 * always open; Knowledge and System collapse until one of their pages is
 * active, so advanced routes stay one click away without making the rail loud.
 */
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
  nextAction: { label: string; href: string };
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
      // Each description is the page's own question (lib/plain-words.ts names);
      // the badges count with lib/counts.ts: Work = work items waiting for
      // your decision, Decide = decisions waiting for you.
      { label: "Today", href: "/", nextAction: { label: "Review decisions", href: "/reviews" }, icon: "home", description: "Start with what needs attention, then see what is moving." },
      { label: "Projects", href: "/projects", nextAction: { label: "Choose a project", href: "#project-scope" }, icon: "grid", description: "Follow a project from its recorded goals to work, dependencies and evidence." },
      { label: "Work", href: "/work", nextAction: { label: "Review blocked work", href: "/work?lens=table&filter=blocked" }, icon: "board", match: ["/work/", "/flow"], badge: "work", description: "Follow progress and resolve what is holding work up." },
      { label: "Decide", href: "/reviews", nextAction: { label: "Review next decision", href: "#decision-queue" }, icon: "gavel", badge: "decide", description: "Review the recommendation, understand the tradeoff and choose the next step." },
      { label: "Live", href: "/live", nextAction: { label: "Review active work", href: "/work?lens=table&filter=running" }, icon: "pulse", description: "Follow the latest recorded activity from your agents." },
    ],
  },
  {
    id: "growth",
    label: "Growth",
    collapsible: false,
    items: [
      { label: "Portfolio", href: "/apps", nextAction: { label: "Choose an app", href: "#apps-list" }, icon: "chart", match: ["/apps/"], description: "Revenue, downloads, traffic and ads across every app." },
      { label: "Growth", href: "/growth", nextAction: { label: "Compare growth gaps", href: "#growth-coverage" }, icon: "target", description: "Find what limits growth and where better evidence would help." },
      { label: "Results", href: "/loops", nextAction: { label: "Review app results", href: "#app-results" }, icon: "loop", description: "See what changed in each app and whether it helped." },
      { label: "Ads", href: "/ads", nextAction: { label: "Review campaigns", href: "#campaign-results" }, icon: "coins", description: "Ad spend, installs and cost per install." },
    ],
  },
  {
    id: "knowledge",
    label: "Knowledge",
    collapsible: true,
    items: [
      { label: "Memory", href: "/memory", nextAction: { label: "Review proposed memories", href: "#memory-review" }, icon: "brain", match: ["/map/memory"], description: "Review proposed memories and explore the evidence behind them." },
      { label: "Rules", href: "/standing-law", nextAction: { label: "Find a rule", href: "#accepted-rules" }, icon: "law", description: "Rules agents follow without asking." },
      { label: "Sources", href: "/sources", nextAction: { label: "Review connections", href: "#source-connections" }, icon: "source", description: "Where knowledge comes from." },
    ],
  },
  {
    id: "system",
    label: "System",
    collapsible: true,
    items: [
      { label: "Agents", href: "/executors", nextAction: { label: "See active work", href: "/live" }, icon: "users", description: "See which agents are available and what they are working on." },
      { label: "Coordination", href: "/traffic", nextAction: { label: "Review blocked work", href: "/work?lens=table&filter=blocked" }, icon: "route", description: "Find work waiting on another agent or a shared resource." },
      { label: "Map", href: "/map", nextAction: { label: "Explore skills and tools", href: "#capability-map" }, icon: "map", match: ["/map/"], description: "Explore the skills, tools and connections Compound can use." },
      { label: "Routing", href: "/routing", nextAction: { label: "Review model decisions", href: "/reviews" }, icon: "sliders", description: "Review model choices, usage and proposed changes." },
      { label: "Settings", href: "/settings", nextAction: { label: "Review permissions", href: "/settings?section=safety-permissions#settings-safety-permissions" }, icon: "sliders", description: "Permissions, safety, connections and console health." },
      { label: "Views", href: "/representation", nextAction: { label: "Explore available views", href: "#view-library" }, icon: "grid", description: "Explore the ways Compound can explain your work." },
    ],
  },
];

function matches(pathname: string, route: { href: string; match?: string[] }): boolean {
  if (route.href === "/") return pathname === "/";
  return pathname === route.href || (route.match ?? []).some((prefix) => pathname.startsWith(prefix));
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
  return undefined;
}
