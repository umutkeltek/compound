/**
 * Plain-language glossary (EP-U1): one-liners for every term and review area
 * an operator sees. The design law: if a term needs a glossary to be
 * understood, any UI showing the term must carry its one-liner in place.
 * Single source for tooltips/subtitles across the console.
 */

export const AREA_GLOSSARY: Record<string, string> = {
  "external-repo-intake": "Repos or frameworks you shared, awaiting your keep / reference / reject decision.",
  "route-promotions": "Model lanes that performed well and are proposed for an upgraded role.",
  "route-remorse": "A routing choice that looks wrong in hindsight — review whether the lane should be demoted.",
  "panel-verdicts": "Judge-panel results waiting to be applied to memory.",
  "synthesis-contradictions": "The memory sweep found claims that disagree; someone must rule.",
  "synthesis-sweeps": "Reports from the background pass that clusters, contradicts, and corroborates memory.",
  "seed-corroborations": "Something you once said just turned out to matter — a later outcome corroborates it. Promote it or leave it parked.",
  "job-health": "Background jobs (crons, daemons) that failed or look unhealthy.",
  "autonomy-grant-mint": "Job-health work is piling up on your click — decide whether the system may admit it on its own once it has proven itself.",
  "source-control-cleanup": "Git hygiene chores (dirty files, stray artifacts) queued for cleanup.",
  "distiller-improvements": "The extraction organ proposing upgrades to its own method.",
  "distiller-requests": "Asks for the distiller to (re)process specific material.",
  "capability-lifecycle-proposals": "Proposals to revise, narrow, or retire a skill — mined from your real sessions. Nothing changes without you.",
  "capability-evaluation-intake": "Shared repos queued for a deeper capability evaluation before any adoption.",
  "observation-candidates": "Raw observations that arrived through the intake door, not yet trusted.",
  "source-observation-verdicts": "Verdicts on observed sources waiting for your confirmation.",
  "product-action-cards": "Suggested next moves on a product loop, derived from live signals.",
};

export const TERM_GLOSSARY: Record<string, string> = {
  proposed: "Suggested. Nothing runs until you add it to work.",
  admitted: "Queued. It may be picked up, but nothing runs yet.",
  ready: "Prepared; waiting for an executor to claim it.",
  claimed: "An executor took it (with a lease so two agents can't collide).",
  running: "An executor is working on it now.",
  in_progress: "An executor is working on it now.",
  verifying: "Being proof-checked before close.",
  blocked: "Waiting on a dependency.",
  held: "Parked for your judgment. Nothing happens until you act.",
  completed: "Done, with proof on record.",
  failed: "Did not succeed; closed.",
  cancelled: "Dropped; closed without running.",
  review_required: "Waiting on your decision.",
  needs_review: "Raised for your attention by evidence, not by a schedule.",
  receipt: "Proof a job ran: what was touched, what it cost, what came out.",
  transition: "The ledger entry: the system moved from state A to B because of this work.",
  candidate: "Arrived but not trusted yet. Everything is born a candidate.",
  promote: "Becomes real memory agents can recall. Needs a recorded reason.",
  park: "Keep it, decide later. The safe default.",
};

/**
 * Move-type plain language for the live transitions ticker (DESIGN.md §9.6).
 * Mirrors decision-registry.ts's MOVE_TYPES labels but is duplicated rather
 * than imported: decision-registry.ts pulls in `@/lib/compound`
 * (node:child_process/fs), so it is server-only, while this glossary is read
 * from client components. move-type-drift-smoke.mjs guards decision-registry
 * against the Go enum; keep this map's five keys identical to that list if it
 * ever changes.
 */
export const MOVE_TYPE_LABELS: Record<string, string> = {
  outward_delta: "Outward delta",
  memory_delta: "Memory delta",
  rule_delta: "Rule delta",
  control_plane_repair: "Control-plane repair",
  audit_only: "Audit only",
};

export const MOVE_TYPE_GLOSSARY: Record<string, string> = {
  outward_delta: "Shipped a real product change — the outward-facing move.",
  memory_delta: "Wrote new memory the system can recall later.",
  rule_delta: "Changed a rule, contract, or doctrine the system follows.",
  control_plane_repair: "Fixed the control plane itself — no product or memory change.",
  audit_only: "Recorded for the audit trail; no state moved.",
};

export function areaPlain(area: string): string {
  return AREA_GLOSSARY[area] ?? "A review area awaiting a decision.";
}

export function movePlain(moveType: string): string {
  return MOVE_TYPE_GLOSSARY[moveType] ?? "A recorded move in the work's lifecycle.";
}
