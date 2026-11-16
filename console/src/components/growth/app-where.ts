/**
 * Where an app lives and runs: its links in the product dependency graph
 * (`compound system graph --kind product --json`): where its code is, where
 * it is deployed and what it calls. The graph spells some apps differently
 * from the app list (hyphen or underscore), so a link counts when either end
 * matches the app's id or any of its aliases, compared without case,
 * hyphens or underscores. Adapted from the retired Projects page's
 * dependency table.
 *
 * Pure: no reads, no clock (pass `nowMs`).
 */

type Row = { readonly [field: string]: unknown };

const object = (value: unknown): Row => (value && typeof value === "object" && !Array.isArray(value) ? (value as Row) : {});
const text = (value: unknown): string => (typeof value === "string" ? value.trim() : "");

/** The relations the page names first, in this order; the rest are counted. */
export const WHERE_RELATIONS = ["code_in", "deployed_on", "calls"] as const;

const RELATION_WORDS: Record<string, string> = { code_in: "Code in", deployed_on: "Deployed on", calls: "Calls" };

export type WhereLink = {
  relation: string;
  /** "Code in", "Deployed on", "Calls" */
  label: string;
  /** the other end, by its label */
  target: string;
  /** "observed" (seen in the code or on the machine) or "declared" (written down) */
  confidence: string;
  /** kept from an earlier read, not seen in the latest one */
  carried: boolean;
  evidence: string;
};

export type AppWhereModel = {
  /** null when the graph read has no edge list: unknown, never "none" */
  links: WhereLink[] | null;
  /** links of other kinds (uses, sold through, …), counted, not listed */
  other: { relation: string; count: number }[];
  /** whole days since the inventory was observed, or null when not reported */
  observedDaysAgo: number | null;
  observedAt: string;
  /** "fresh", "stale" or "missing", as the graph reports it */
  state: string;
};

function key(id: string): string {
  return id.trim().toLowerCase().replace(/^product:/, "").replace(/[^\p{L}\p{N}]+/gu, "");
}

export function appWhereModel(graph: unknown, ids: readonly string[], nowMs: number): AppWhereModel {
  const source = object(graph);
  const snapshot = object(source.snapshot);
  const observedAt = text(snapshot.observed_at);
  const at = Date.parse(observedAt);
  const observedDaysAgo = Number.isFinite(at) ? Math.max(0, Math.floor((nowMs - at) / 86_400_000)) : null;
  const state = text(snapshot.state);
  if (!Array.isArray(source.edge_list)) return { links: null, other: [], observedDaysAgo, observedAt, state };
  const wanted = new Set(ids.map(key).filter(Boolean));
  const mine = (end: string) => end.toLowerCase().startsWith("product:") && wanted.has(key(end));
  const links: WhereLink[] = [];
  const other = new Map<string, number>();
  for (const raw of source.edge_list) {
    const edge = object(raw);
    const from = text(edge.from);
    const to = text(edge.to);
    if (!mine(from) && !mine(to)) continue;
    const relation = text(edge.rel);
    if (!(WHERE_RELATIONS as readonly string[]).includes(relation)) {
      const title = text(edge.rel_title) || relation.replace(/_/g, " ");
      other.set(title, (other.get(title) ?? 0) + 1);
      continue;
    }
    const outward = mine(from);
    links.push({
      relation,
      label: RELATION_WORDS[relation] ?? relation,
      target: outward ? text(edge.to_label) || to : text(edge.from_label) || from,
      confidence: text(edge.confidence) || "not reported",
      carried: edge.carried === true,
      evidence: text(edge.evidence),
    });
  }
  const order = (relation: string) => (WHERE_RELATIONS as readonly string[]).indexOf(relation);
  links.sort((a, b) => order(a.relation) - order(b.relation) || a.target.localeCompare(b.target));
  return { links, other: [...other].map(([relation, count]) => ({ relation, count })).sort((a, b) => b.count - a.count), observedDaysAgo, observedAt, state };
}
