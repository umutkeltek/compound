import type { LensGraph, LensNode, LensEdge } from "./map/lens";

export type RecordValue = Record<string, unknown>;
const object = (value: unknown): RecordValue => value && typeof value === "object" && !Array.isArray(value) ? value as RecordValue : {};
const text = (value: unknown): string => typeof value === "string" ? value.trim() : "";
const rows = (value: unknown): RecordValue[] => Array.isArray(value) ? value.map(object) : [];
const strings = (value: unknown): string[] => Array.isArray(value) ? [...new Set(value.map(text).filter(Boolean))] : [];
const terminal = new Set(["completed", "failed", "cancelled"]);
const attention = new Set(["held", "blocked", "needs_review"]);
export const UNLINKED_SCOPE = "__unlinked__";

export type ProjectSummary = {
  id: string; label: string; kind: string; total: number; open: number; attention: number; completed: number;
};
export type ProjectWork = {
  id: string; title: string; outcome: string; success: string; state: string; owner: string;
  next: string; reason: string; context: string; policy: string; receipt: string; transition: string;
  evidence: string[]; checks: string[]; attempts: string[]; boundaries: string[]; createdAt: string;
};
export type WorkspaceModel = {
  projects: ProjectSummary[]; project: ProjectSummary | null; work: ProjectWork[];
  selected: ProjectWork | null; requestedMissing: boolean; unlinked: number; workKnown: boolean;
};

export function projectLabel(id: string): string {
  if (id === UNLINKED_SCOPE) return "Unlinked work";
  const value = id.replace(/^(product|project):/, "").replace(/\\/g, "/");
  const leaf = value.includes("/") ? value.split("/").filter(Boolean).at(-1) || value : value;
  return leaf.replace(/[-_]/g, " ");
}

/** Explicit scope and declared surfaces only. Names and task prose never assign a project. */
export function workScopeRefs(work: RecordValue): string[] {
  const scope = object(work.scope);
  const refs: string[] = [];
  for (const [field, prefix] of [["project_id", "project:"], ["product_id", "product:"]] as const) {
    const value = text(scope[field]);
    if (value) refs.push(value.includes(":") ? value : prefix + value);
  }
  const surface = text(work.canonical_surface);
  if (surface && !surface.startsWith("packet:")) refs.push(surface);
  return [...new Set(refs)].filter((ref) => !/[\p{Cc}]/u.test(ref));
}

export function projectHref(scope = "", work = "", view = ""): string {
  const params = new URLSearchParams();
  if (scope) params.set("scope", scope);
  if (work) params.set("work", work);
  if (view === "map") params.set("view", view);
  return "/projects" + (params.size ? "?" + params.toString() : "");
}

function mapWork(work: RecordValue, summary: RecordValue): ProjectWork {
  const ids = object(work.ids);
  const action = object(work.action_context);
  const id = text(work.id) || text(work.work_id);
  const outcome = text(work.desired_outcome);
  return {
    id, title: (text(action.title) || text(work.title) || outcome || text(work.success_predicate) || id).replace(/\s+/g, " ").slice(0, 180),
    outcome, success: text(work.success_predicate), state: text(work.state) || "unknown",
    owner: text(work.owner),
    next: text(summary.next_action), reason: text(summary.open_reason) || text(work.review_trigger),
    context: text(ids.context_pack_id) || text(work.context_pack_id),
    policy: text(work.policy_decision_id), receipt: text(ids.receipt_id) || text(work.receipt_id) || text(summary.receipt_id),
    transition: text(ids.transition_id) || text(work.transition_id) || text(summary.transition_id),
    evidence: strings(work.evidence_refs), checks: strings(work.verification_ids),
    attempts: strings(work.run_attempt_ids), boundaries: strings(work.non_goals),
    createdAt: text(work.created_at),
  };
}

export function buildWorkspace(
  workValue: unknown, inventoryValue: unknown, benchValue: unknown, scopeId = "", selectedId = "",
): WorkspaceModel {
  const source = object(workValue);
  const workKnown = Array.isArray(source.work);
  const workRows = rows(source.work);
  const inventory = object(inventoryValue);
  const projects = new Map<string, ProjectSummary>();
  const add = (id: string, label = "", kind = "") => {
    if (!id || /[\p{Cc}]/u.test(id)) return;
    const current = projects.get(id);
    if (current) { if (label) current.label = label; return; }
    projects.set(id, {
      id, label: label || projectLabel(id),
      kind: kind || (id.startsWith("product:") ? "product" : id.startsWith("project:") ? "project" : "area"),
      total: 0, open: 0, attention: 0, completed: 0,
    });
  };
  // The inventory is an observed registry, not a new project authority.
  for (const resource of rows(inventory.resources)) {
    if (text(resource.kind) === "product") add(text(resource.id), text(resource.label), "product");
  }
  let unlinked = 0;
  for (const work of workRows) {
    if (!text(work.id) && !text(work.work_id)) continue;
    const refs = workScopeRefs(work);
    if (!refs.length) unlinked++;
    for (const ref of refs.length ? refs : [UNLINKED_SCOPE]) {
      add(ref);
      const project = projects.get(ref)!;
      const state = text(work.state);
      project.total++;
      if (!terminal.has(state)) project.open++;
      if (attention.has(state)) project.attention++;
      if (state === "completed") project.completed++;
    }
  }
  const bench = object(benchValue);
  const summaries = new Map([...rows(bench.open), ...rows(bench.recent)].map((r) => [text(r.work_id) || text(r.id), r]));
  const rank: Record<string, number> = { held: 0, blocked: 0, needs_review: 0, running: 1, claimed: 2, verifying: 3, ready: 4, admitted: 4, proposed: 5 };
  const work = workRows.filter((row) => {
    if (!scopeId) return false;
    const refs = workScopeRefs(row);
    return scopeId === UNLINKED_SCOPE ? refs.length === 0 : refs.includes(scopeId);
  }).map((row) => mapWork(row, summaries.get(text(row.id) || text(row.work_id)) || {}))
    .filter((row) => !!row.id)
    .sort((a, b) => (rank[a.state] ?? 6) - (rank[b.state] ?? 6) || b.createdAt.localeCompare(a.createdAt) || a.id.localeCompare(b.id));
  const selected = selectedId ? work.find((w) => w.id === selectedId) || null : work[0] || null;
  return {
    projects: [...projects.values()].sort((a, b) => a.id === UNLINKED_SCOPE ? 1 : b.id === UNLINKED_SCOPE ? -1 : b.attention - a.attention || b.open - a.open || a.label.localeCompare(b.label)),
    project: projects.get(scopeId) || null,
    // Send full evidence only for the selected work; large project lists stay small.
    work: work.map((w) => w === selected ? w : { ...w, outcome: "", success: "", reason: "", context: "", policy: "", receipt: "", transition: "", evidence: [], checks: [], attempts: [], boundaries: [] }),
    selected,
    requestedMissing: !!selectedId && workKnown && !selected, unlinked, workKnown,
  };
}

/** References show attachment, never success or current process activity. */
export function workGraph(work: ProjectWork): LensGraph {
  const source = "compound work list --json";
  const center = "work:" + work.id;
  const nodes: LensNode[] = [{
    id: center, kind: "work", label: work.title, sublabel: "Work state: " + work.state,
    accent: "blue", position: { x: 300, y: 180 }, sources: [source], raw: { work_id: work.id, state: work.state },
  }];
  const edges: LensEdge[] = [];
  const add = (kind: string, id: string, label: string, relation: string, incoming = false) => {
    if (!id || !label || nodes.some((n) => n.id === kind + ":" + id)) return;
    const index = nodes.length - 1;
    const nodeId = kind + ":" + id;
    nodes.push({
      id: nodeId, kind, label, sublabel: kind === "outcome" ? "Recorded on this work" : id,
      accent: kind === "policy" ? "violet" : "gray",
      position: { x: incoming ? 0 : 620, y: index * 100 }, sources: [source], raw: { reference: id, relationship: relation },
    });
    edges.push({ id: center + ":" + nodeId, source: incoming ? nodeId : center, target: incoming ? center : nodeId, kind: "reference", label: relation });
  };
  add("outcome", work.id, work.outcome, "aims for", true);
  add("owner", work.owner, work.owner, "accountable owner");
  add("context", work.context, "Agent context", "context attached", true);
  add("policy", work.policy, "Permission decision", "governed by", true);
  add("receipt", work.receipt, "Recorded result", "result reference");
  add("transition", work.transition, "Recorded change", "change reference");
  const refs = [
    ...work.attempts.map((ref) => ({ kind: "attempt", ref, label: "Recorded attempt", relation: "attempt reference" })),
    ...work.evidence.map((ref) => ({ kind: "evidence", ref, label: "Supporting evidence", relation: "evidence attached" })),
    ...work.checks.map((ref) => ({ kind: "check", ref, label: "Verification reference", relation: "check attached" })),
  ];
  for (const r of refs) if (nodes.length < 12) add(r.kind, r.ref, r.label, r.relation);
  const omitted = Math.max(0, refs.length - edges.filter((e) => /attempt|evidence|check/.test(e.label || "")).length);
  return {
    lens: "work-spine", nodes, edges, generatedAt: "", sources: [source],
    note: "Only recorded references are drawn. Attached checks and attempts do not prove success or live activity." + (omitted ? " " + omitted + " more references remain in the inspector." : ""),
  };
}

export type DependencyLink = {
  from: string; fromLabel: string; to: string; toLabel: string; relation: string; confidence: string; evidence: string; carried: boolean;
};
export function dependencyLinks(value: unknown, scopeId: string): { links: DependencyLink[]; omitted: number } {
  const source = object(value);
  const all = rows(source.edge_list).filter((e) => text(e.from) === scopeId || text(e.to) === scopeId);
  const links = all.slice(0, 12).map((e) => ({
    from: text(e.from), fromLabel: text(e.from_label) || text(e.from), to: text(e.to), toLabel: text(e.to_label) || text(e.to),
    relation: text(e.rel_title) || text(e.rel), confidence: text(e.confidence) || "not reported",
    evidence: text(e.evidence), carried: e.carried === true,
  }));
  return { links, omitted: all.length - links.length };
}
