import type { AttentionReport } from "@/lib/attention";
import { attentionNeedsYouGroups } from "@/lib/attention";
import { arr, str, type Json, type ReadState } from "@/lib/compound";
import { unit } from "@/lib/counts";
import { humanizeProductId } from "@/lib/app-identity";
import { mapGrowthModel } from "@/lib/growth";
import { appHref, decideHref, workItemHref } from "@/lib/links";
import { plainArea, plainState, plainText, productTitle } from "@/lib/plain-words";
import { workTitle } from "@/lib/work-title";
import type { WorkProjectIndex } from "@/lib/work-project";

/**
 * What search finds beyond the page names: his apps, the decisions that wait
 * for him and open work, each with the words he sees on its own page and the
 * link that page's owner builds (lib/links.ts). Built from reads the pages
 * already cache; a read with no copy yet is "loading", never an empty list.
 */
export type SearchKind = "page" | "view" | "app" | "decision" | "work";

export type SearchEntry = {
  kind: SearchKind;
  /** What he reads first: the name or title, as its own page shows it. */
  label: string;
  /** Its context: which app or part it is about, its state. */
  sub: string;
  href: string;
  /** Lower-case words search matches against (label, context, names he may type). */
  terms: string;
};

export type SearchGroup = { state: ReadState; entries: SearchEntry[] };

export type SearchIndex = {
  apps: SearchGroup;
  decisions: SearchGroup;
  work: SearchGroup;
  /** The decision that matters most (attention's top), offered as a quick view; null when none waits or not read yet. */
  top: SearchEntry | null;
};

export type SearchReads = {
  growth: { status: ReadState; value: Json | null };
  attention: { status: ReadState; report: AttentionReport | null };
  work: { status: ReadState; value: Json | null };
  /** Open work grouped by app (P0 resolver); null while any of its reads has no copy. */
  projects: WorkProjectIndex | null;
};

const terms = (...parts: unknown[]): string =>
  parts
    .flat()
    .filter((part): part is string => typeof part === "string" && part.length > 0)
    .join(" ")
    .toLowerCase();

function appsGroup(read: SearchReads["growth"]): SearchGroup {
  const model = read.value ? mapGrowthModel(read.value) : null;
  if (!model) return { state: read.value ? "unreachable" : read.status, entries: [] };
  const entries = [...model.apps]
    .sort((a, b) => a.name.localeCompare(b.name))
    .map((app): SearchEntry => ({
      kind: "app",
      label: app.name,
      sub: "App",
      href: appHref(app.productId),
      terms: terms(app.name, app.aliases, app.productId.replace(/^product:/, "")),
    }));
  return { state: read.status, entries };
}

function decisionsGroup(read: SearchReads["attention"]): SearchGroup {
  if (!read.report) return { state: read.status === "live" || read.status === "stale" ? "unreachable" : read.status, entries: [] };
  const entries = attentionNeedsYouGroups(read.report).map((group): SearchEntry => {
    const titled = productTitle(plainText(group.operator_summary || ""), humanizeProductId);
    const label = titled.text || "A decision that waits for you";
    const area = plainArea(group.area);
    const count = typeof group.count === "number" && group.count > 0 ? unit(group.count, "item") : "";
    return {
      kind: "decision",
      label,
      sub: [area, count].filter(Boolean).join(" · "),
      href: decideHref({ decision: group.key }),
      terms: terms(label, area, group.area, titled.id),
    };
  });
  return { state: read.status, entries };
}

function workGroup(read: SearchReads["work"], projects: WorkProjectIndex | null): SearchGroup {
  if (!read.value) return { state: read.status, entries: [] };
  const names = new Map((projects?.projects ?? []).map((p) => [p.id, p.kind === "none" ? "" : p.name] as const));
  const entries = arr(read.value.open)
    .map((row): SearchEntry | null => {
      const id = str(row.work_id) || str(row.id);
      if (!id) return null;
      const title = workTitle(row, 100);
      const project = names.get(projects?.byWork[id] ?? "") ?? "";
      const state = str(row.state);
      return {
        kind: "work",
        label: title,
        sub: [project, state ? plainState(state) : ""].filter(Boolean).join(" · "),
        href: workItemHref(id),
        terms: terms(title, project, id),
      };
    })
    .filter((entry): entry is SearchEntry => entry !== null);
  return { state: read.status, entries };
}

/** The quick view "Most important decision": attention's top decision, as Today ranks it. */
function topDecision(read: SearchReads["attention"], decisions: SearchGroup): SearchEntry | null {
  const report = read.report;
  if (!report) return null;
  const waiting = attentionNeedsYouGroups(report);
  const top = report.top && report.top.band === "needs_you" ? report.top : waiting[0];
  if (!top) return null;
  const href = decideHref({ decision: top.key });
  const shown = decisions.entries.find((entry) => entry.href === href);
  return {
    kind: "view",
    label: "Most important decision",
    sub: shown?.label ?? "",
    href,
    terms: terms("most important decision top what needs me first", shown?.terms),
  };
}

export function buildSearchIndex(reads: SearchReads): SearchIndex {
  const decisions = decisionsGroup(reads.attention);
  return {
    apps: appsGroup(reads.growth),
    decisions,
    work: workGroup(reads.work, reads.projects),
    top: topDecision(reads.attention, decisions),
  };
}
