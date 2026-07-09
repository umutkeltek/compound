/*
 * Briefing data-shaping — pure, server-safe, deterministic (no LLM, no fs, no
 * subprocess). The page (src/app/page.tsx) fetches projections; this module
 * turns them into the exact shapes the briefing components render. Ranking is
 * NOT here — it is the server-side job of `compound attention --json`; this only
 * translates already-ranked/already-projected data into view models.
 *
 * Client-side leverage ranking (`selectHighestLeverageGate`) was deleted in
 * Phase 2; only the age helpers and these projection-to-view-model mappers live
 * here now.
 */

import type { AttentionGroup } from "@/lib/attention";
import { arr, num, str, type Json } from "@/lib/compound";
import { toDate } from "@/lib/time";
import { GENERIC_ACTION_ALLOWLIST, RATIONALE_REQUIRED_AREAS } from "@/lib/generic-actions";
import type { ContinueRequest } from "@/components/ContinueButton";
import type { StateTone } from "@/components/kit";

/* ---------- age helpers (presentation) ---------- */

export function humanAge(value: unknown): string {
  const date = value instanceof Date ? value : value ? new Date(value as string | number) : null;
  if (!date || Number.isNaN(date.getTime())) return "";
  const seconds = Math.max(0, Math.floor((Date.now() - date.getTime()) / 1000));
  if (seconds < 90) return "now";
  if (seconds < 90 * 60) return `${Math.round(seconds / 60)}m`;
  if (seconds < 36 * 3600) return `${Math.round(seconds / 3600)}h`;
  return `${Math.round(seconds / 86400)}d`;
}

export function isOld(value: unknown): boolean {
  const date = value instanceof Date ? value : value ? new Date(value as string | number) : null;
  if (!date || Number.isNaN(date.getTime())) return false;
  return Date.now() - date.getTime() >= 36 * 3600 * 1000;
}

/* ---------- THE headline's inline governed act ----------
 * Maps one AttentionGroup.action onto a governed /api/decision/execute payload.
 * Returns null when there is NO safe inline act — honest: the headline then
 * shows only a "Review" deep-link, never a fake button. The mapping mirrors how
 * the Reviews page already routes each action shape, so the same click runs the
 * same governed argv through the same three gates.
 */

export type HeadlineActDescriptor = {
  /** which /api/decision/execute pathway resolves + gates the argv server-side. */
  pathway: "bespoke" | "generic";
  area: string;
  itemId: string;
  /** bespoke pathway only. */
  option?: string;
  /** generic pathway only. */
  actionId?: string;
  label: string;
  /** display-only preview (server re-resolves the real argv before running). */
  argv: string[];
  requiresConfirmation: boolean;
  rationaleRequired: boolean;
};

function flagValue(argv: string[], flag: string): string {
  const i = argv.indexOf(flag);
  return i >= 0 ? str(argv[i + 1]) : "";
}

export function headlineAct(group: AttentionGroup | null): HeadlineActDescriptor | null {
  const action = group?.action;
  if (!group || !action) return null;
  // Copy-only backend forms (e.g. panel per-item `/bin/sh -lc`) are never run
  // from a card — honest, no button.
  if (action.kind === "copy_only") return null;

  const argv = arr(action.argv).map(String);
  if (argv.length < 2 || argv[0] !== "compound") return null;
  const bare = argv.slice(1); // drop leading "compound"
  const label = str(action.label) || "Act";
  const requiresConfirmation = Boolean(action.requires_confirmation);

  // Panel run apply — bespoke `panel-apply` (itemId = the run id).
  if (str(action.id) === "panel_run_apply" || (bare[0] === "panel" && bare[1] === "run" && bare.includes("--apply"))) {
    const runId = flagValue(bare, "--plan");
    if (!/^prun_[A-Za-z0-9_]+$/.test(runId)) return null;
    return { pathway: "bespoke", area: "panel-apply", itemId: runId, option: "apply", label, argv, requiresConfirmation, rationaleRequired: false };
  }

  // Panel grouped verdict record — bespoke `panel-verdict-record-group`.
  if (str(action.id) === "panel_verdict_record_group") {
    return { pathway: "bespoke", area: "panel-verdict-record-group", itemId: str(group.key), option: "record", label, argv, requiresConfirmation, rationaleRequired: false };
  }

  // Workbench dispatch actions don't carry a stable work_id in the attention
  // group (the guard in decision-execute re-checks the live dispatch target),
  // so we don't inline them here — the Continue panel already owns that seat.
  if (str(action.id).startsWith("workbench.")) return null;

  // Generic governed pathway — only for areas the backend has CERTIFIED
  // (generic-actions GENERIC_ACTION_ALLOWLIST). An un-certified area would 403
  // on click, so we render no button for it (honest).
  const area = str(group.area);
  if (GENERIC_ACTION_ALLOWLIST[area]) {
    return {
      pathway: "generic",
      area,
      itemId: str(group.key),
      actionId: str(action.id),
      label,
      argv,
      requiresConfirmation,
      rationaleRequired: RATIONALE_REQUIRED_AREAS.has(area),
    };
  }

  return null;
}

/* ---------- read-only pipeline strip (7 envelope stages) ----------
 * The envelope spine at board scale (DESIGN.md §5 signature moment A). Counts
 * come straight from the workbench projection's `counts` — WIP stages from the
 * live `by_state` split, throughput stages from the cumulative totals. Read-only
 * here; the Work screen owns the governed moves.
 */

export type PipelineStage = {
  key: string;
  label: string;
  count: number;
  tone: StateTone;
  /** true for the executing stage — gets the one beating node. */
  live?: boolean;
  hint: string;
};

export function pipelineStages(workbench: Json | null): PipelineStage[] {
  const counts = (workbench?.counts ?? {}) as Json;
  const byState = (counts.by_state ?? {}) as Record<string, number>;
  const st = (k: string): number => num(byState[k]);
  const execute = st("running") + st("verifying") || num(counts.in_progress);
  const terminal = num(counts.terminal) || st("completed") + st("failed") + st("cancelled");

  return [
    { key: "signal", label: "Signal", count: num(counts.admission_previews), tone: "draft", hint: "Suggested work offered, not yet proposed" },
    { key: "propose", label: "Propose", count: st("proposed"), tone: "draft", hint: "Proposed — awaiting admission" },
    { key: "admit", label: "Admit", count: st("admitted"), tone: "seam", hint: "The operator's amber seat — admit into work" },
    { key: "claim", label: "Claim", count: st("ready") + st("claimed"), tone: "queued", hint: "Ready or claimed by an executor" },
    { key: "execute", label: "Execute", count: execute, tone: "live", live: execute > 0, hint: "Running or verifying" },
    { key: "receipt", label: "Receipt", count: st("completed"), tone: "done", hint: "Completed with a receipt" },
    { key: "learn", label: "Learn", count: terminal, tone: "done", hint: "Reached a Transition — fed learning" },
  ];
}

/* ---------- autonomy posture (topbar chip + KPI tile) ---------- */

export type AutonomyPosture = {
  installed: boolean;
  level: string;
  mode: string;
  /** short plain-language posture, e.g. "closure over expansion". */
  posture: string;
  /** one-sentence doctrine gloss. */
  detail: string;
  tone: "good" | "seam";
};

export function autonomyPosture(auth: Json | null): AutonomyPosture {
  const installed = Boolean(auth?.installed);
  const level = str(auth?.autonomy_level);
  const mode = str(auth?.mode);
  const posture = mode ? mode.replace(/_/g, " ") : "unknown";
  const detail =
    mode === "closure_over_expansion"
      ? "Closes open work before it expands scope — proposes, never self-authorizes."
      : installed
        ? "Governed by imported operator authority."
        : "No operator authority imported — fail-closed.";
  return { installed, level: level || "—", mode, posture, detail, tone: installed && level ? "good" : "seam" };
}

/* ---------- self-healing (home doctor hygiene) ---------- */

export type HygieneFlag = {
  key: string;
  label: string;
  count: number;
  command: string;
  tone: "seam" | "crit";
};

export type SelfHealing = { ok: boolean; flags: HygieneFlag[]; checkedDirs: string };

export function selfHealing(doctor: Json | null): SelfHealing {
  if (!doctor) return { ok: true, flags: [], checkedDirs: "" };
  const oh = (doctor.operational_hygiene ?? {}) as Json;
  const flags: HygieneFlag[] = [];

  const hotWork = num(oh.terminal_hot_work_count);
  if (hotWork > 0) {
    flags.push({ key: "hot-work", label: "Terminal work not archived", count: hotWork, command: "compound home archive-history --kind work", tone: "seam" });
  }
  const staleLeases = num(oh.stale_lease_count);
  if (staleLeases > 0) {
    flags.push({ key: "stale-leases", label: "Stale executor leases", count: staleLeases, command: "compound home archive-history --kind lease", tone: "seam" });
  }
  if (doctor.manifest_ok === false) {
    flags.push({ key: "manifest", label: "Home manifest drift", count: 1, command: "compound home doctor", tone: "crit" });
  }
  const dirsOk = num(doctor.dirs_ok);
  const dirsTotal = num(doctor.dirs_total);
  if (dirsTotal > 0 && dirsOk < dirsTotal) {
    flags.push({ key: "dirs", label: "Home directories missing", count: dirsTotal - dirsOk, command: "compound home doctor", tone: "crit" });
  }
  if (doctor.generated_state_in_repo_source === true) {
    flags.push({ key: "repo-state", label: "Generated state inside repo", count: 1, command: "compound home doctor", tone: "crit" });
  }

  const ok = flags.length === 0 && oh.ok !== false;
  return { ok, flags, checkedDirs: dirsTotal ? `${dirsOk}/${dirsTotal} dirs` : "" };
}

/* ---------- Continue panel derivation (kit-styled ContinueCard) ----------
 * Pure port of the ContinuePanel state machine so the briefing can render a
 * kit-token Continue card without touching the shared ContinuePanel component
 * (which the Work screen also renders). ContinueButton is reused for the act.
 */

export type ContinueTone = StateTone;

export type ContinueModel = {
  headline: string;
  body: string;
  tone: ContinueTone;
  chips: { label: string; active: boolean }[];
  actor: string;
  request: ContinueRequest | null;
  blockedOn: string[];
};

function itemWorkId(item: Json): string {
  return str(item.work_id) || str(item.id) || str(item.displayId);
}

function continueActionLabel(action: string): string {
  switch (action) {
    case "admit":
      return "add suggested work";
    case "prepare":
      return "prepare work";
    case "claim":
      return "start work";
    case "advance":
      return "continue work";
    case "close":
      return "finish with proof";
    default:
      return action ? action.replace(/_/g, " ") : "continue work";
  }
}

function continueRequest(workId: string, actor: string, note: string): ContinueRequest {
  return {
    area: "work-continue",
    itemId: workId,
    option: "continue",
    fields: { actor },
    command: ["compound", "work", "continue", "--actor", actor, "--expect-work", workId, "--until-wait", "--json"],
    note,
  };
}

export function deriveContinue(workbench: Json | null, open: Json[]): ContinueModel {
  const next = (workbench?.dispatch_next ?? {}) as Json;
  const canRun = Boolean(next.can_run);
  const action = str(next.action);
  const actor = str(next.actor) || "codex";
  const workId = str(next.work_id);
  const waitReason = str(next.wait_reason);
  const blockedOn = arr(next.blocked_on).map(String);

  const admissionPreviews = arr(workbench?.admission_previews);
  const continuableAdmissionPreviews = admissionPreviews.filter(
    (p) =>
      Boolean(str(p?.work_id)) &&
      Boolean(p?.policy_allowed) &&
      !p?.requires_operator &&
      !str(p?.admission_blocked_reason) &&
      str(p?.staleness) === "fresh",
  );
  const reviewCardIds = new Set(open.flatMap((w) => arr(w.review_cards).map((c) => str(c.id)).filter(Boolean)));
  const blockedAdmissionPreviewCount = admissionPreviews.filter(
    (p) => !continuableAdmissionPreviews.includes(p) && !reviewCardIds.has(str(p?.action_context?.source_id)),
  ).length;
  const admissionPreviewWorkId = str(continuableAdmissionPreviews[0]?.work_id);

  const inboxItems = open.filter((w) => ["proposed", "admitted"].includes(str(w.state)));
  const inboxWorkId = itemWorkId(inboxItems[0] ?? {});
  const intakeCount = inboxItems.length + continuableAdmissionPreviews.length;
  const readyCount = open.filter((w) => str(w.state) === "ready").length;
  const operatorGateCount =
    open.reduce((total, w) => total + arr(w.review_cards).length, 0) + blockedAdmissionPreviewCount;

  let headline = "Nothing to continue";
  let body = "Compound sees no safe next move right now.";
  let tone: ContinueTone = "queued";
  let request: ContinueRequest | null = null;

  if (canRun && workId) {
    headline = `Next — ${continueActionLabel(action)}`;
    body = `Compound can move ${workId} until the next real wait.`;
    tone = "live";
    request = continueRequest(workId, actor, "Moves safe prep/start/finish steps until the next real wait. Does not run task commands.");
  } else if (readyCount > 0) {
    headline = "Ready to start";
    body = `${readyCount} item${readyCount === 1 ? "" : "s"} can be started by an executor.`;
    tone = "done";
  } else if (intakeCount > 0) {
    headline = "Prepare suggested work";
    body = `${intakeCount} suggested task${intakeCount === 1 ? "" : "s"} can be prepared before an executor starts.`;
    tone = "seam";
    const target = inboxWorkId || admissionPreviewWorkId;
    request = target
      ? continueRequest(target, actor, "Adds, prepares, and starts one suggested task until the next real wait. Does not run task commands.")
      : null;
  } else if (operatorGateCount > 0) {
    headline = "Needs your decision";
    body =
      operatorGateCount === 1
        ? "1 item needs judgment, private input, or an unblock step."
        : `${operatorGateCount} items need judgment, private input, or an unblock step.`;
    tone = "seam";
  } else if (waitReason) {
    headline = "Waiting";
    body = waitReason.replace(/_/g, " ");
    tone = "queued";
  }

  return {
    headline,
    body,
    tone,
    actor,
    request,
    blockedOn: canRun ? [] : blockedOn,
    chips: [
      { label: `${readyCount} ready`, active: readyCount > 0 },
      { label: `${intakeCount} inbox`, active: intakeCount > 0 },
      { label: `${operatorGateCount} needs me`, active: operatorGateCount > 0 },
    ],
  };
}

/* ---------- oldest-age helper for the attention pill ---------- */

export function oldestCreatedAt(groups: AttentionGroup[]): Date | undefined {
  return groups
    .map((g) => toDate(g.oldest_created_at))
    .filter((d): d is Date => Boolean(d))
    .sort((a, b) => a.getTime() - b.getTime())[0];
}
