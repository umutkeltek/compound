/**
 * Where everything in the console lives, named the way the operator asks:
 * six sections, each holding its pages as tabs. The sidebar, the tab row
 * under every page header, the breadcrumb, the page header's default name
 * and purpose, and search all read this one file, so a name cannot differ
 * between them. Client-safe and JSX-free: Nav maps icon keys to glyphs.
 *
 * Every existing address stays a page here (or redirects in next.config.ts).
 * A page that left the menu stays `unlisted`: named for the breadcrumb and the
 * header, but no tab and no search row. Pages agents use more than he does sit
 * in Settings' quiet "back room".
 */
export type NavIconKey = "home" | "chart" | "board" | "users" | "brain" | "sliders";

export type NavPage = {
  /** The tab label, and the page's name in the header, breadcrumb and search. */
  label: string;
  href: string;
  /** One plain sentence: what this page shows. PageHeader's default lead on the page itself. */
  description: string;
  /** The header button. Only when it leads somewhere he is not already looking; null = none. */
  nextAction: { label: string; href: string } | null;
  /** Path prefixes of this page's detail pages (an app, a work item, a record). */
  match?: readonly string[];
  /** Earlier names. Pages pass `PageHeader name="<earlier name>"`, and he may still type them. */
  aliases?: readonly string[];
  /** Other words he might type for this page (search only). */
  keywords?: readonly string[];
  /** A page agents use more than he does: a quieter tab in the back room. */
  backRoom?: boolean;
  /** Left the menu: no tab and no search row; the address still works. */
  unlisted?: boolean;
};

export type NavSectionId = "today" | "apps" | "work" | "agents" | "knowledge" | "settings";

export type NavSection = {
  id: NavSectionId;
  /** The sidebar label. */
  label: string;
  /** The question this section answers, in his words. */
  question: string;
  icon: NavIconKey;
  pages: readonly NavPage[];
};

/** Back compatible name for one page of the registry. */
export type NavItem = NavPage;

/** The label of the Settings tab cluster that holds pages agents use. */
export const BACK_ROOM_LABEL = "Back room";

export const NAV_SECTIONS: readonly NavSection[] = [
  {
    id: "today",
    label: "Today",
    question: "What needs you?",
    icon: "home",
    pages: [
      {
        label: "Today",
        href: "/",
        description: "Start with what needs attention, then see what is moving.",
        nextAction: { label: "Review decisions", href: "/reviews" },
        keywords: ["home", "needs me", "waiting for me", "inbox", "start"],
      },
      {
        label: "All decisions",
        href: "/reviews",
        description: "Every decision that waits for you, with the recommendation and what happens next.",
        nextAction: { label: "Review next decision", href: "#decision-queue" },
        aliases: ["Decide"],
        keywords: ["decisions", "review", "approve", "verdict", "blocked on me", "queue"],
      },
    ],
  },
  {
    id: "apps",
    label: "Apps",
    question: "How are your apps doing?",
    icon: "chart",
    pages: [
      {
        label: "All apps",
        href: "/apps",
        description: "Revenue, downloads, traffic and ads across every app.",
        nextAction: { label: "Choose an app", href: "#apps-list" },
        match: ["/apps/"],
        aliases: ["Portfolio"],
        keywords: ["overview", "portfolio", "revenue", "proceeds", "downloads", "money"],
      },
      {
        label: "Growth gaps",
        href: "/growth",
        description: "Find what limits growth and where better evidence would help.",
        nextAction: { label: "Compare growth gaps", href: "#growth-coverage" },
        aliases: ["Growth", "Growth audit"],
        keywords: ["growth", "coverage", "what limits growth"],
      },
      {
        label: "Results",
        href: "/loops",
        description: "See what changed in each app and whether it helped.",
        nextAction: { label: "Review app results", href: "#app-results" },
        aliases: ["Feedback loops"],
        keywords: ["did it help", "experiments", "changes", "before and after"],
      },
      {
        label: "Ads",
        href: "/ads",
        description: "Ad spend, installs and cost per install.",
        nextAction: { label: "Review campaigns", href: "#campaign-results" },
        keywords: ["campaigns", "spend", "installs", "cost per install", "advertising"],
      },
    ],
  },
  {
    id: "work",
    label: "Work",
    question: "What is being done?",
    icon: "board",
    pages: [
      {
        label: "Work",
        href: "/work",
        description: "Follow progress and resolve what is holding work up.",
        nextAction: { label: "Review blocked work", href: "/work?lens=table&filter=blocked" },
        match: ["/work/", "/flow"],
        keywords: ["tasks", "work items", "blocked", "running", "stuck", "board", "list", "on hold"],
      },
      {
        label: "Projects",
        href: "/projects",
        description: "Follow a project from its recorded goals to work, dependencies and evidence.",
        nextAction: { label: "Choose a project", href: "#project-scope" },
        unlisted: true,
      },
    ],
  },
  {
    id: "agents",
    label: "Agents",
    question: "Who is working, and what is stuck?",
    icon: "users",
    pages: [
      {
        label: "Working now",
        href: "/live",
        description: "Follow the latest recorded activity from your agents.",
        nextAction: { label: "Review active work", href: "/work?lens=table&filter=running" },
        aliases: ["Live"],
        keywords: ["live", "now", "activity", "running"],
      },
      {
        label: "All agents",
        href: "/executors",
        description: "See which agents are available and what they are working on.",
        nextAction: { label: "See active work", href: "/live" },
        aliases: ["Agents"],
        keywords: ["agents", "who works", "available"],
      },
      {
        label: "Waiting on each other",
        href: "/traffic",
        description: "Find work waiting on another agent or a shared resource.",
        nextAction: { label: "Review blocked work", href: "/work?lens=table&filter=blocked" },
        aliases: ["Coordination"],
        keywords: ["coordination", "handoff", "blocked", "shared resource"],
      },
    ],
  },
  {
    id: "knowledge",
    label: "Knowledge",
    question: "What does Compound know and follow?",
    icon: "brain",
    pages: [
      {
        label: "Memory",
        href: "/memory",
        description: "Review proposed memories and explore the evidence behind them.",
        nextAction: { label: "Review proposed memories", href: "#memory-review" },
        match: ["/map/memory"],
        keywords: ["remember", "recall", "proposed memories", "what it knows"],
      },
      {
        label: "Rules",
        href: "/standing-law",
        description: "Rules agents follow without asking.",
        nextAction: { label: "Find a rule", href: "#accepted-rules" },
        aliases: ["Standing law"],
        keywords: ["standing rules", "policy", "allowed", "what agents may do"],
      },
      {
        label: "Sources",
        href: "/sources",
        description: "Where knowledge comes from.",
        nextAction: { label: "Review connections", href: "#source-connections" },
        keywords: ["connections", "intake", "evidence", "feeds", "data sources"],
      },
    ],
  },
  {
    id: "settings",
    label: "Settings",
    question: "What may Compound do, and how is it set up?",
    icon: "sliders",
    pages: [
      {
        label: "Settings",
        href: "/settings",
        description: "Permissions, safety, connections and console health.",
        nextAction: { label: "Review permissions", href: "/settings?section=safety-permissions#settings-safety-permissions" },
        keywords: ["permissions", "safety", "autonomy", "health", "stop agents", "preferences"],
      },
      {
        label: "Models and cost",
        href: "/routing",
        description: "Review model choices, usage and proposed changes.",
        nextAction: { label: "Review model decisions", href: "/reviews" },
        aliases: ["Routing"],
        keywords: ["models", "routing", "usage", "providers", "spend"],
      },
      {
        label: "Skills and tools",
        href: "/map",
        description: "Explore the skills, tools and connections Compound can use.",
        nextAction: { label: "Explore skills and tools", href: "#capability-map" },
        match: ["/map/"],
        aliases: ["Map"],
        keywords: ["map", "capabilities", "skills", "tools"],
        backRoom: true,
      },
      {
        label: "Evidence history",
        href: "/map/lineage",
        description: "Trace where a record came from and which decisions use it.",
        nextAction: { label: "Find a record", href: "#lineage-search" },
        match: ["/map/lineage/"],
        aliases: ["Lineage"],
        keywords: ["lineage", "trace", "where did this come from"],
        backRoom: true,
      },
      {
        label: "View library",
        href: "/representation",
        description: "Explore the ways Compound can explain your work.",
        nextAction: { label: "Explore available views", href: "#view-library" },
        aliases: ["Views"],
        keywords: ["views", "explanations"],
        backRoom: true,
      },
    ],
  },
];

export type NavPlace = { section: NavSection; page: NavPage };

/** How closely a page claims a path: 0 = not at all; an exact address beats the longest detail prefix. */
function claim(pathname: string, page: NavPage): number {
  if (pathname === page.href) return Number.MAX_SAFE_INTEGER;
  if (page.href === "/") return 0;
  let best = 0;
  for (const prefix of page.match ?? []) {
    if (pathname.startsWith(prefix) && prefix.length > best) best = prefix.length;
  }
  return best;
}

/** The section and page an address belongs to, or null for an address the console does not have. */
export function placeForPath(pathname: string): NavPlace | null {
  let found: NavPlace | null = null;
  let score = 0;
  for (const section of NAV_SECTIONS) {
    for (const page of section.pages) {
      const s = claim(pathname, page);
      if (s > score) {
        score = s;
        found = { section, page };
      }
    }
  }
  return found;
}

/** The page for a `PageHeader name` (current label or an earlier name). */
export function placeForName(name: string | undefined): NavPlace | null {
  if (!name) return null;
  for (const section of NAV_SECTIONS) {
    for (const page of section.pages) {
      if (page.label === name || (page.aliases ?? []).includes(name)) return { section, page };
    }
  }
  return null;
}

/** The pages shown as tabs in a section (a single-page section shows none). */
export function sectionTabs(section: NavSection): { main: NavPage[]; backRoom: NavPage[] } {
  const listed = section.pages.filter((page) => !page.unlisted);
  if (listed.length < 2) return { main: [], backRoom: [] };
  return { main: listed.filter((page) => !page.backRoom), backRoom: listed.filter((page) => page.backRoom) };
}

/** Where a sidebar section row leads: its first listed page. */
export function sectionHref(section: NavSection): string {
  return section.pages.find((page) => !page.unlisted)?.href ?? section.pages[0].href;
}

/** True when this address is inside the section. */
export function sectionIsActive(pathname: string, section: NavSection): boolean {
  return placeForPath(pathname)?.section.id === section.id;
}

/** True when this address is the page or one of its detail pages. */
export function routeIsActive(pathname: string, route: NavPage): boolean {
  return placeForPath(pathname)?.page === route;
}

/** The page an address belongs to (kept for callers of the earlier registry). */
export function navItemForPath(pathname: string): NavItem | undefined {
  return placeForPath(pathname)?.page;
}

/** Every page he can reach from search, with its section. */
export function searchablePages(): NavPlace[] {
  return NAV_SECTIONS.flatMap((section) =>
    section.pages.filter((page) => !page.unlisted).map((page) => ({ section, page })),
  );
}
