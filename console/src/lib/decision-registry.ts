import { arr, type Json, str } from "@/lib/compound";
import { memoryReviewHref } from "@/lib/links";
import { MOVE_TYPES } from "@/lib/move-types";
import { memoryChoice } from "@/lib/plain-words";
import { shortText, targetLabel, uniqueBoundaries } from "@/lib/plain-language";
import { WORK_ID_PATTERN } from "@/lib/work-id";
import { selectedContinueRevision, selectedContinueView } from "@/lib/work-selected-continue";

/*
 * The Decision Registry — the heart of the console-as-control-surface.
 *
 * Each entry declares how a decision AREA renders in plain language and how a
 * one-click approval maps to a `compound` CLI invocation. The keystone: most
 * review items already carry `actions[0].argv` in their projection, so the CLI
 * command is read from the (server-resolved) projection rather than hardcoded —
 * the registry only adds the human layer (headline/options/fields) and bounded
 * argv construction for areas that do not carry a projected command.
 *
 * Adding a new area = adding one entry here; the generic decision views and
 * the /api/decision/execute route pick it up automatically.
 */

/* ---------- types ---------- */

export type Accent = "blue" | "green" | "orange" | "pink" | "violet" | "red" | "gray";

export type FieldType = "text" | "textarea" | "number" | "select" | "datetime-local";

export type FieldSpec = {
  name: string;
  type: FieldType;
  label: string;
  required?: boolean;
  placeholder?: string;
  min?: number;
  max?: number;
  step?: number;
  defaultValue?: string;
  options?: { value: string; label: string }[];
  /** Only render this field when one of these option values is selected. */
  showForOptions?: string[];
};

export type DecisionOption = {
  value: string;
  label: string;
  tone: Accent;
  description: string;
  /** Display-only argv preview for the caption (what this option will run). */
  argv?: string[] | null;
};

/**
 * Serializable subset of a spec — the only part that may cross the
 * server→client boundary into the interactive form (functions/fs stay
 * server-side). Extracted via `toFormSpec`.
 */
export type DecisionFormSpec = {
  area: string;
  options: DecisionOption[];
  fields: FieldSpec[];
  defaultOption?: string;
};

export type PlainLanguage = {
  headline: string;
  body: string;
  evidence?: string[];
  boundaries?: string[];
  chips?: { label: string; tone: Accent }[];
  /** The consequence pair (EP-U6): what acting unblocks vs what waiting does. */
  consequences?: { approve: string; hold: string };
};

export type ExecContext = {
  itemId: string;
  item: Json;
  option: string;
  fields: Record<string, string>;
  expectedRevision?: string;
};

export type DecisionEffectKind = "apply" | "record" | "track";

export type DecisionAreaSpec = {
  area: string;
  title: string;
  glyph: string;
  accent: Accent;
  source: "reviews" | "workbench" | "orient";

  /** Operator-facing effect class. Permission/recording is never described as task success. */
  effectKind?: DecisionEffectKind;

  /** This owner exposes a write-free server preview before confirmation. */
  supportsPreview?: boolean;

  /** Non-preview execution must carry the client's explicit confirmation marker. */
  requiresConfirmation?: boolean;

  /** Optimistic-concurrency token rendered with the item and rechecked on submit. */
  revisionFor?: (item: Json) => string;

  /** Defense in depth: allowed argv verb prefixes (checked after buildArgv). */
  argvAllowlist: string[][];

  /** Validate the item id shape for this area. */
  idPattern: RegExp;

  /** Render the item in plain language. */
  plainLanguage: (item: Json) => PlainLanguage;

  /** Decision options — rendered as labeled buttons, never a bare dropdown. */
  options: DecisionOption[];

  /** Dynamic options computed from the item (e.g. work state transitions). */
  optionsFor?: (item: Json) => DecisionOption[];

  /** Default option value (visual default under strict-start). */
  defaultOption?: string;

  /** Form fields beyond the option selector. */
  fields: FieldSpec[];

  /** Build argv (without leading "compound"). Defaults to item.actions[0].argv. */
  buildArgv?: (ctx: ExecContext) => string[] | null;

  /** Resolve a fresh argv preview for the caption (client-side, display only). */
  previewArgv?: (item: Json) => string[] | null;
  /** Some orient-backed actions still require a fresh server projection hit. */
  requiresResolvedItem?: boolean;
};

/* ---------- shared helpers ---------- */

/** Read argv from the projection's actions[0].argv, dropping a leading "compound". */
export function argvFromActions(item: Json): string[] | null {
  const action = arr(item.actions)[0];
  if (!action) return null;
  const argv = arr(action.argv).map(String);
  if (argv.length < 2) return null;
  const start = argv[0] === "compound" ? 1 : 0;
  return argv.slice(start);
}

/** Clamp a confidence-like value to [0, 1]; 0 for non-finite input. */
export function clamp01(value: unknown): number {
  const n = Number(value);
  if (!Number.isFinite(n)) return 0;
  return Math.min(1, Math.max(0, n));
}

/** Does argv begin with one of the allowed verb prefixes? */
export function argvMatchesAllowlist(argv: string[], allowlist: string[][]): boolean {
  return allowlist.some((prefix) =>
    prefix.length > 0 && prefix.every((seg, i) => argv[i] === seg),
  );
}

/* ---------- work-advance helpers (state machine mirrors internal/work) ---------- */

export const WORK_TRANSITIONS: Record<string, string[]> = {
  proposed: ["admitted", "ready", "held", "cancelled"],
  admitted: ["ready", "held", "cancelled"],
  ready: ["claimed", "held", "cancelled"],
  claimed: ["running", "ready", "held", "cancelled"],
  running: ["verifying", "blocked", "held", "failed", "completed"],
  verifying: ["completed", "running", "failed"],
  blocked: ["running", "cancelled", "failed"],
  held: ["ready", "cancelled"],
};

const WORK_TERMINAL = new Set(["completed", "failed", "cancelled"]);

const WORK_OPTION_META: Record<string, { label: string; tone: Accent; description: string }> = {
  admitted: { label: "Add to work", tone: "green", description: "Accept this suggested task." },
  ready: { label: "Prepare", tone: "blue", description: "Make it ready for an executor." },
  claimed: { label: "Start", tone: "violet", description: "Assign this work." },
  running: { label: "Continue", tone: "blue", description: "Move active work forward." },
  verifying: { label: "Check", tone: "blue", description: "Move into proof checking." },
  blocked: { label: "Mark blocked", tone: "orange", description: "Waiting on a dependency." },
  held: { label: "Needs me", tone: "orange", description: "Park for human judgment." },
  completed: { label: "Complete", tone: "green", description: "Done with proof; closes the work." },
  failed: { label: "Mark failed", tone: "red", description: "Did not succeed; closes the work." },
  cancelled: { label: "Cancel", tone: "gray", description: "Drop the work; audit-only close." },
};

// MOVE_TYPES is the move-type registry shared between this server-only module
// and the client-safe move-types module. The Go side (internal/envelope MoveTypes)
// is the enum authority; the move-types module mirrors the labels and one-liners
// byte-for-byte. This module imports the values so the work-advance select
// dropdown stays in sync with the client's glide path (decision-registry is
// server-only because it pulls in @/lib/compound — node:child_process/fs).
// Re-export removed: the canonical home is now @/lib/move-types; downstream
// consumers that needed MOVE_TYPES from this module should import it directly
// from there. (The work-advance move-type select below uses the import above.)

/** Build the work argv for a target state. Terminal targets use `work close`. */
export function workArgvFor(
  workId: string,
  target: string,
  summary: string,
  moveType: string,
): string[] | null {
  // Claim requires an executor identity; the operator-facing console cannot synthesize one.
  if (target === "claimed") return null;
  if (WORK_TERMINAL.has(target)) {
    const argv = ["work", "close", workId, "--outcome", target, "--by", "operator-console"];
    argv.push("--move-type", target === "cancelled" ? "audit_only" : moveType || "control_plane_repair");
    if (summary.trim()) argv.push("--summary", summary.trim());
    return argv;
  }
  return ["work", "advance", workId, "--to", target, "--by", "operator-console"];
}

/** Human options for a work item given its current state. */
export function workOptionsFor(item: Json): DecisionOption[] {
  const state = str(item.state);
  const id = str(item.work_id) || str(item.id);
  return (WORK_TRANSITIONS[state] ?? []).flatMap((target) => {
    const meta = WORK_OPTION_META[target] ?? {
      label: target,
      tone: "gray" as Accent,
      description: target,
    };
    const argv = workArgvFor(id, target, "", "control_plane_repair");
    if (!argv) return [];
    return [{
      value: target,
      label: meta.label,
      tone: meta.tone,
      description: meta.description,
      argv,
    }];
  });
}

/* ---------- the registry ---------- */

export const REGISTRY: Record<string, DecisionAreaSpec> = {

  "capability-lifecycle-proposals": {
    area: "capability-lifecycle-proposals",
    title: "Capability lifecycle proposal",
    glyph: "C",
    accent: "green",
    source: "reviews",
    effectKind: "track",
    supportsPreview: true,
    requiresConfirmation: true,
    argvAllowlist: [["capability", "lifecycle-review"]],
    idPattern: /^clp_[A-Za-z0-9_-]+$/,
    revisionFor: (item) => {
      const card = item.decision_card && typeof item.decision_card === "object" ? (item.decision_card as Json) : {};
      const meta = card.meta && typeof card.meta === "object" ? (card.meta as Json) : {};
      return str(meta.proposal_hash);
    },
    optionsFor: (item) => {
      const card = item.decision_card && typeof item.decision_card === "object" ? (item.decision_card as Json) : {};
      return arr(card.choices)
        .filter((choice): choice is Json => Boolean(choice) && typeof choice === "object")
        .map((choice) => {
          const value = str(choice.id);
          const tone: Accent = value === "reject" ? "red" : value === "defer" ? "orange" : "green";
          return {
            value,
            label: str(choice.label) || value,
            tone,
            description: str(choice.effect),
          };
        })
        .filter((option) => ["open_work", "defer", "reject", "resume_open_work"].includes(option.value));
    },
    options: [],
    plainLanguage: (item) => {
      const card = item.decision_card && typeof item.decision_card === "object" ? (item.decision_card as Json) : {};
      const reasons = arr(card.why).filter((reason): reason is Json => Boolean(reason) && typeof reason === "object");
      const boundaries = arr(card.boundary).map(String).filter(Boolean);
      const claim = str(card.claim) || str(item.operator_summary) || "Review this capability lifecycle proposal.";
      const body = reasons.map((reason) => str(reason.detail)).filter(Boolean).join(" ");
      return {
        headline: claim,
        body: shortText(body || str(item.next_action), 320),
        evidence: arr(item.evidence_refs).map(String).slice(0, 12),
        boundaries,
        consequences: {
          approve: "your disposition is recorded; open work creates one proposed WorkEnvelope while defer/reject create no work",
          hold: "the proposal remains review evidence and no capability authority changes",
        },
      };
    },
    fields: [
      {
        name: "rationale",
        type: "textarea",
        label: "Rationale",
        required: true,
        placeholder: "Why is this the right disposition? This becomes durable decision provenance.",
        showForOptions: ["open_work", "defer", "reject"],
      },
      {
        name: "defer_until",
        type: "datetime-local",
        label: "Revisit at",
        required: true,
        showForOptions: ["defer"],
      },
    ],
    buildArgv: (ctx) => {
      if (ctx.option === "resume_open_work") {
        const action = arr(ctx.item.actions)
          .find((candidate): candidate is Json => Boolean(candidate) && typeof candidate === "object" && str((candidate as Json).id) === ctx.option);
        const projected = arr(action?.argv).map(String).filter(Boolean);
        if (projected.length < 2 || projected[0] !== "compound") return null;
        const bare = projected.slice(1);
        const valueFor = (flag: string) => {
          const index = bare.indexOf(flag);
          return index >= 0 ? str(bare[index + 1]) : "";
        };
        if (
          valueFor("--proposal") !== ctx.itemId ||
          valueFor("--expected-hash") !== str(ctx.expectedRevision) ||
          valueFor("--decision") !== "open_work" ||
          !valueFor("--rationale") ||
          !valueFor("--actor")
        ) return null;
        return bare;
      }
      const expectedHash = str(ctx.expectedRevision);
      const rationale = str(ctx.fields.rationale).trim();
      if (!expectedHash || !rationale) return null;
      const argv = [
        "capability", "lifecycle-review",
        "--proposal", ctx.itemId,
        "--expected-hash", expectedHash,
        "--decision", ctx.option,
        "--rationale", rationale,
        "--actor", "operator-console",
        "--json",
      ];
      if (ctx.option === "defer") {
        const local = str(ctx.fields.defer_until);
        if (!local) return null;
        const parsed = new Date(local);
        if (!Number.isFinite(parsed.getTime())) return null;
        argv.push("--defer-until", parsed.toISOString());
      }
      return argv;
    },
  },
  /* ----------------------------------------------------------------------
   * autonomy-grant-mint (operator correction of record, 2026-07-11). The
   * governed autonomy grant becomes a plain-language card, not a CLI command
   * to copy. The item is SYNTHESIZED server-side (internal/decisions/
   * grant_mint.go) only when director job_health work is waiting and no valid
   * grant exists; this entry adds the human layer and rebuilds the exact
   * `decisions record` argv from the operator's three editable thresholds.
   * `decisions record` has NO --preview (it writes), so this owner does NOT
   * set supportsPreview: it confirms via requiresConfirmation, and the confirm
   * dialog's Technical-details block is the exact-command disclosure where the
   * machine terms (scope/class/thresholds) live — never in the card copy.
   * Client threshold checks mirror internal/decisions/record.go for immediate
   * feedback; the Go validator remains the real gate.
   * -------------------------------------------------------------------- */
  "autonomy-grant-mint": {
    area: "autonomy-grant-mint",
    title: "Autonomy grant",
    glyph: "A",
    accent: "green",
    source: "reviews",
    effectKind: "record",
    requiresConfirmation: true,
    argvAllowlist: [["decisions", "record"]],
    idPattern: /^agm_[a-z0-9_]+$/,
    options: [
      {
        value: "grant",
        label: "Grant it",
        tone: "green",
        description:
          "Record an autonomy grant so proven job-health work enters the queue on its own. Every entry still writes a receipt and one switch halts it.",
      },
    ],
    defaultOption: "grant",
    plainLanguage: (item) => {
      const cls = grantMintClass(item);
      const plain = cls.replace(/_/g, "-");
      const waiting = grantMintWaitingCount(item);
      const noun = waiting === 1 ? "item is" : "items are";
      return {
        // What never changes — the calm guardrail, led with before the tradeoff.
        headline: `Let proven ${plain} work enter the queue on its own?`,
        body:
          "Anything external, destructive, irreversible, financial, or security-related still always comes to you. No grant can override that.",
        consequences: {
          // if you act → what changes; if you wait → why it is here.
          approve: `after the system has closed enough ${plain} items cleanly (you set the bar below), new ones enter the queue on their own — every entry writes a receipt, and one switch halts it`,
          hold: `${waiting} ${plain} ${noun} waiting; each currently needs your click`,
        },
      };
    },
    fields: [
      { name: "min_terminal_samples", type: "number", label: "Proven completions required", required: true, min: 1, step: 1, defaultValue: "5" },
      { name: "min_window_days", type: "number", label: "Within days", required: true, min: 1, step: 1, defaultValue: "7" },
      { name: "max_failures", type: "number", label: "Failures allowed", required: true, min: 0, step: 1, defaultValue: "0" },
    ],
    buildArgv: (ctx) => {
      if (ctx.option !== "grant") return null;
      const samples = grantMintInt(ctx.fields.min_terminal_samples, 5);
      const windowDays = grantMintInt(ctx.fields.min_window_days, 7);
      const failures = grantMintInt(ctx.fields.max_failures, 0);
      if (samples === null || windowDays === null || failures === null) return null;
      // Mirror internal/decisions/record.go: samples>0, window>0,
      // 0 <= failures < samples. The Go validator remains the real gate.
      if (samples <= 0 || windowDays <= 0) return null;
      if (failures < 0 || failures >= samples) return null;
      const cls = grantMintClass(ctx.item);
      return [
        "decisions", "record",
        "--kind", "grant",
        "--scope", "autonomy-evidence-threshold",
        "--auto-admit-class", cls,
        "--min-terminal-samples", String(samples),
        "--min-window-days", String(windowDays),
        "--max-failures", String(failures),
        "--reviewed-by", "operator-console",
        "--title", `Let proven ${cls.replace(/_/g, "-")} work enter the queue on its own`,
        "--json",
      ];
    },
    previewArgv: (item) => {
      const cls = grantMintClass(item);
      return [
        "decisions", "record", "--kind", "grant", "--scope", "autonomy-evidence-threshold",
        "--auto-admit-class", cls, "--min-terminal-samples", "5", "--min-window-days", "7",
        "--max-failures", "0", "--reviewed-by", "operator-console",
        "--title", `Let proven ${cls.replace(/_/g, "-")} work enter the queue on its own`, "--json",
      ];
    },
  },
  /* ----------------------------------------------------------------------
   * job-health (operator-blocking fix, 2026-07-11). Modeled directly on the
   * autonomy-grant-mint entry above — same mechanism, zero Decide component
   * changes: the decision views are generic over the REGISTRY, so a
   * bespoke spec here is all that's needed. The broken generic "job-health"
   * entry in generic-actions.ts stays UNTOUCHED and simply stops being
   * reachable from the card act ("a bespoke spec always wins" —
   * decision-execute.ts's specFor(area) fork). Before this entry, the
   * record command ran against the UNFILLED draft YAML (decision still the
   * TODO placeholder) because the console collected no decision at all — the
   * operator could not complete a single job-health review. The Go side
   * (internal/authority/job_health_review.go FillJobHealthReviewDraft) now
   * fills the draft from `--decision`/`--rationale` and writes it back to the
   * SAME path before running the existing validate+record path, so this
   * owner only has to build that one governed command. `job-health-review`
   * has no --preview (it writes the review file), so — like grant-mint —
   * this owner does NOT set supportsPreview; it confirms via
   * requiresConfirmation instead. The five choices are plain labels over the
   * Go enum (repair_required|parked|disabled|no_action|owner_action_done);
   * rationale is a required textarea. Client-side option/rationale checks
   * mirror the Go validator for immediate feedback — the Go validator
   * remains the real gate.
   * -------------------------------------------------------------------- */
  "source-control-cleanup": {
    area: "source-control-cleanup",
    title: "Source-control cleanup review",
    glyph: "S",
    accent: "orange",
    source: "reviews",
    effectKind: "record",
    requiresConfirmation: true,
    argvAllowlist: [["authority", "dirty-review"]],
    idPattern: /^scr_[a-z0-9]+$/,
    options: [],
    optionsFor: (item) => sourceControlReviewYAMLPath(item) ? SOURCE_CONTROL_DECISIONS : [],
    defaultOption: "owner_action_required",
    plainLanguage: (item) => ({
      headline: str(item.operator_summary) || "This repository has uncommitted changes. What should happen?",
      body: "Choose the owner disposition. This records the decision; it never stages, discards, commits, stashes, or pushes files.",
      evidence: arr(item.evidence_refs).map(String).slice(0, 8),
      consequences: { approve: "the selected disposition and rationale become the durable review record", hold: "nothing changes; the dirty-state review remains open" },
    }),
    fields: [{ name: "rationale", type: "textarea", label: "Rationale", required: true, placeholder: "Why is this the right disposition?" }],
    buildArgv: (ctx) => {
      const path = sourceControlReviewYAMLPath(ctx.item);
      const rationale = str(ctx.fields.rationale).trim();
      if (!path || !rationale || !SOURCE_CONTROL_DECISIONS.some((choice) => choice.value === ctx.option)) return null;
      return ["authority", "dirty-review", path, "--decision", ctx.option, "--rationale", rationale, "--json"];
    },
  },

  "job-health": {
    area: "job-health",
    title: "Job health review",
    glyph: "J",
    accent: "orange",
    source: "reviews",
    effectKind: "record",
    requiresConfirmation: true,
    argvAllowlist: [["authority", "job-health-review"]],
    idPattern: /^jhp_[a-z0-9]+$/,
    options: [],
    optionsFor: (item) => {
      // No discoverable review.yaml sidecar ⇒ no compatible workflow for THIS
      // item (mirrors route-promotions/grant-mint: zero options, not options
      // with a null argv) — buildArgv could never succeed for any of the five
      // choices either, so the owner is honestly unavailable rather than
      // offering dead buttons.
      const path = jobHealthReviewYAMLPath(item);
      if (!path) return [];
      return JOB_HEALTH_DECISIONS.map((choice) => ({
        value: choice.value,
        label: choice.label,
        tone: choice.tone,
        description: choice.description,
        argv: ["authority", "job-health-review", path, "--decision", choice.value, "--rationale", "…", "--json"],
      }));
    },
    defaultOption: "parked",
    plainLanguage: (item) => {
      const jobLabel = str(item.context_label) || "this scheduled job";
      return {
        headline: str(item.operator_summary) || `The scheduled job ${jobLabel} needs a decision.`,
        body:
          shortText(str(item.next_action), 200) ||
          "Choose what happens next for this failing scheduled job.",
        evidence: arr(item.evidence_refs).map(String).slice(0, 8),
        chips: [{ label: shortText(jobLabel, 34), tone: "violet" as Accent }],
        consequences: {
          approve:
            str(item.operator_unlock) ||
            "your decision + rationale become the durable job-health record for this job",
          hold: "nothing changes; it stays in this queue and the job keeps failing silently",
        },
      };
    },
    fields: [
      {
        name: "rationale",
        type: "textarea",
        label: "Rationale",
        required: true,
        placeholder: "Why?",
      },
    ],
    buildArgv: (ctx) => {
      const path = jobHealthReviewYAMLPath(ctx.item);
      const rationale = str(ctx.fields.rationale).trim();
      if (!path || !rationale) return null;
      if (!JOB_HEALTH_DECISIONS.some((choice) => choice.value === ctx.option)) return null;
      return ["authority", "job-health-review", path, "--decision", ctx.option, "--rationale", rationale, "--json"];
    },
  },
  "external-repo-intake": {
    area: "external-repo-intake",
    title: "External repo intake",
    glyph: "E",
    accent: "blue",
    source: "reviews",
    argvAllowlist: [["capability", "external-intake-review"]],
    idPattern: /^extrepo_[a-z0-9]{6,}$/,
    plainLanguage: (item) => {
      const hint = str(item.graph_decision_hint);
      const owner = str(item.owner);
      const strength = item.graph_evidence_strength;
      return {
        headline: targetLabel(item),
        body:
          shortText(str(item.next_action), 200) ||
          (hint ? `System suggests: ${hint}.` : "An external repo source awaits your intake decision."),
        evidence: arr(item.evidence_refs).map(String).slice(0, 6),
        boundaries: uniqueBoundaries(item),
        chips: [
          ...(owner ? [{ label: shortText(owner, 34), tone: "violet" as Accent }] : []),
          ...(strength ? [{ label: `evidence ${strength}`, tone: "orange" as Accent }] : []),
        ],
        consequences: {
          approve:
            "your decision + rationale go on the permanent record and steer what happens to this repo — it is never cloned, installed, or run by this card",
          hold: "nothing happens; it stays in this queue",
        },
      };
    },
    options: [
      { value: "keep_needs_review", label: "Keep under review", tone: "orange", description: "No adoption yet; stays in the review queue." },
      { value: "merge_existing", label: "Merge into existing", tone: "green", description: "Adopt specific pieces into an existing owner." },
      { value: "reference_only", label: "Reference only", tone: "blue", description: "Record as reference; no adoption, no graph node." },
      { value: "reject", label: "Reject", tone: "red", description: "Close the review; do not adopt." },
    ],
    defaultOption: "keep_needs_review",
    fields: [
      { name: "rationale", type: "textarea", label: "Rationale", required: true, placeholder: "Write the operator rationale. This becomes the recorded evidence for the decision." },
      { name: "confidence", type: "number", label: "Confidence", required: true, min: 0, max: 1, step: 0.01, defaultValue: "0.70" },
    ],
    buildArgv: (ctx) => argvFromActions(ctx.item),
    previewArgv: (item) => argvFromActions(item),
  },

  "work-advance": {
    area: "work-advance",
    title: "Advance work",
    glyph: "W",
    accent: "blue",
    source: "workbench",
    argvAllowlist: [["work", "advance"], ["work", "close"], ["work", "claim"]],
    idPattern: WORK_ID_PATTERN,
    optionsFor: workOptionsFor,
    options: [],
    plainLanguage: (item) => {
      const id = str(item.work_id) || str(item.id);
      const surface = str(item.canonical_surface);
      const title = surface.startsWith("packet:")
        ? surface.slice("packet:".length).replace(/-/g, " ")
        : str(item.success_predicate) || id;
      const claimer = str(item.claimed_by) || str(item.owner);
      const state = str(item.state);
      const approveByState: Record<string, string> = {
        proposed: "it becomes claimable — an executor may pick it up; nothing runs yet",
        admitted: "it becomes ready for an executor to claim",
        ready: "an executor is assigned and can start",
        claimed: "the executor moves it forward",
        running: "it advances toward proof and close",
        verifying: "it closes with a receipt + transition that feed learning",
        blocked: "it resumes or closes honestly",
        held: "it re-enters the queue",
      };
      return {
        headline: title,
        body: shortText(str(item.next_action) || str(item.desired_outcome), 200),
        chips: claimer ? [{ label: claimer, tone: "violet" as Accent }] : [],
        consequences: {
          approve: approveByState[state] ?? "the job moves one step forward in its lifecycle",
          hold: `nothing changes — it stays ${state || "where it is"}`,
        },
      };
    },
    fields: [
      {
        name: "summary",
        type: "textarea",
        label: "Close summary",
        required: true,
        placeholder: "What was done / why it is closing. Required to complete, fail, or cancel.",
        showForOptions: ["completed", "failed", "cancelled"],
      },
      {
        name: "move_type",
        type: "select",
        label: "Move type",
        defaultValue: "control_plane_repair",
        options: MOVE_TYPES,
        showForOptions: ["completed", "failed"],
      },
    ],
    buildArgv: (ctx) =>
      workArgvFor(
        ctx.itemId,
        ctx.option,
        str(ctx.fields.summary),
        str(ctx.fields.move_type),
      ),
  },

  "work-continue": {
    area: "work-continue",
    title: "Continue work",
    glyph: ">",
    accent: "green",
    source: "workbench",
    argvAllowlist: [["work", "continue"]],
    idPattern: WORK_ID_PATTERN,
    options: [
      {
        value: "continue",
        label: "Continue",
        tone: "green",
        description: "Move safe lifecycle steps until the next real wait.",
        argv: ["work", "continue", "--actor", "codex", "--until-wait", "--json"],
      },
    ],
    plainLanguage: (item) => {
      const id = str(item.work_id) || str(item.id);
      return {
        headline: `Continue ${id}`,
        body: "Move safe lifecycle steps until the next real wait. This does not execute task commands.",
        consequences: {
          approve: "safe bookkeeping steps run until the next real wait — task commands never run from this button",
          hold: "the work waits exactly where it is",
        },
      };
    },
    fields: [],
    buildArgv: (ctx) => [
      "work",
      "continue",
      "--actor",
      str(ctx.fields.actor) || "codex",
      "--expect-work",
      ctx.itemId,
      "--until-wait",
      "--json",
    ],
  },
  "work-selected-continue": {
    area: "work-selected-continue",
    title: "Continue this work",
    glyph: ">",
    accent: "green",
    source: "workbench",
    argvAllowlist: [["work", "continue", "--selected"]],
    idPattern: WORK_ID_PATTERN,
    requiresConfirmation: true,
    revisionFor: (item) => selectedContinueRevision(item, (item.selected_lease ?? null) as Json | null),
    options: [],
    optionsFor: (item) => {
      const view = selectedContinueView(item, (item.selected_lease ?? null) as Json | null);
      if (view.canStart) return [{ value: "continue", label: "Run this work", tone: "green", description: "Admit one detached governed run for this exact work and lease holder." }];
      if (view.canResume) return [{ value: "resume", label: "Resume run", tone: "green", description: "Explicitly start one fresh governed run from verification; no automatic completion." }];
      return [];
    },
    plainLanguage: (item) => ({
      headline: `Continue ${str(item.id)}`,
      body: "Starts at most one governed detached run for this exact work. A launch is not task completion.",
    }),
    fields: [],
    buildArgv: (ctx) => {
      const view = selectedContinueView(ctx.item, (ctx.item.selected_lease ?? null) as Json | null);
      if ((ctx.option === "continue" && !view.canStart) || (ctx.option === "resume" && !view.canResume)) return null;
      return ["work", "continue", "--selected", "--expect-work", ctx.itemId, "--actor", view.actor,
        ...(ctx.option === "resume" ? ["--resume"] : []), "--json"];
    },
  },
  "work-dispatch": {
    area: "work-dispatch",
    title: "Continue work",
    glyph: ">",
    accent: "green",
    source: "workbench",
    argvAllowlist: [["work", "dispatch-step"]],
    idPattern: WORK_ID_PATTERN,
    options: [
      {
        value: "dispatch_step",
        label: "Continue",
        tone: "green",
        description: "Move one safe lifecycle step.",
        argv: ["work", "dispatch-step", "--actor", "codex", "--until-wait"],
      },
    ],
    plainLanguage: (item) => {
      const id = str(item.work_id) || str(item.id);
      return {
        headline: `Continue ${id}`,
        body: "Move one safe lifecycle step. This does not execute task commands.",
        consequences: {
          approve: "one safe bookkeeping step runs — task commands never run from this button",
          hold: "the work waits exactly where it is",
        },
      };
    },
    fields: [],
    buildArgv: (ctx) => [
      "work",
      "dispatch-step",
      "--actor",
      str(ctx.fields.actor) || "codex",
      "--until-wait",
    ],
  },

  "memory-adjudicate": {
    area: "memory-adjudicate",
    title: "Memory adjudication",
    glyph: "M",
    accent: "violet",
    source: "orient",
    requiresResolvedItem: true,
    argvAllowlist: [["memory", "adjudicate"]],
    idPattern: /^cand_[A-Za-z0-9_-]+$/,
    options: [],
    optionsFor: (item) => {
      const id = str(item.id);
      const defs: DecisionOption[] = [
        {
          value: "park",
          label: memoryChoice("park", "memory").label,
          tone: "orange",
          description: memoryChoice("park", "memory").means,
        },
        {
          value: "no_promote",
          label: memoryChoice("no_promote", "memory").label,
          tone: "gray",
          description: memoryChoice("no_promote", "memory").means,
        },
        {
          value: "reject",
          label: memoryChoice("reject", "memory").label,
          tone: "red",
          description: memoryChoice("reject", "memory").means,
        },
        {
          value: "promote",
          label: memoryChoice("promote", "memory").label,
          tone: "green",
          description: `${memoryChoice("promote", "memory").means} Pick why you accept it below.`,
        },
      ];
      return defs.map((o) => ({
        ...o,
        argv: ["memory", "adjudicate", id, "--verdict", o.value],
      }));
    },
    plainLanguage: (item) => {
      const chips: { label: string; tone: Accent }[] = [];
      if (item.confidence != null && item.confidence !== "")
        chips.push({ label: `conf ${item.confidence}`, tone: "orange" });
      if (str(item.derivation)) chips.push({ label: str(item.derivation), tone: "blue" });
      if (str(item.promotion_basis))
        chips.push({ label: `basis: ${str(item.promotion_basis)}`, tone: "green" });
      if (str(item.visibility)) chips.push({ label: str(item.visibility), tone: "gray" });
      const qualifiers = (item.qualifiers ?? {}) as Json;
      const evidence = [
        str(item.source_trace_ref),
        str((item.console_source ?? {}).evidence_ref),
        str(qualifiers.extraction_id),
        str(qualifiers.item_ref),
        str(qualifiers.source),
      ].filter(Boolean);
      return {
        headline: str(item.entity) || str(item.id),
        body: shortText(`${str(item.predicate)}: ${str(item.object)}`, 220),
        evidence,
        chips,
        consequences: {
          approve:
            `${memoryChoice("promote", "memory").label}: ${memoryChoice("promote", "memory").means} ${memoryChoice("park", "memory").label} or ${memoryChoice("reject", "memory").label.toLowerCase()}: agents are never told it.`,
          hold: "It stays a proposal: you can see it here, agents are not told it.",
        },
      };
    },
    defaultOption: "park",
    fields: [
      {
        name: "basis",
        type: "select",
        label: "Why you accept it",
        required: true,
        defaultValue: "explicit_confirmation",
        options: [
          { value: "explicit_confirmation", label: "I confirm this" },
          { value: "curated_page", label: "Curated page" },
          { value: "decision_record", label: "Decision record" },
          { value: "runtime_proof", label: "Runtime proof" },
          { value: "trusted_deterministic_import", label: "Trusted import" },
        ],
        showForOptions: ["promote"],
      },
    ],
    buildArgv: (ctx) => {
      const argv = ["memory", "adjudicate", ctx.itemId, "--verdict", ctx.option];
      if (ctx.option === "promote") {
        const basis = str(ctx.fields.basis) || str(ctx.item.promotion_basis);
        if (basis) argv.push("--basis", basis);
      }
      return argv;
    },
  },

  /* ----------------------------------------------------------------------
   * standing-law-candidate (operator-legibility fix, 2026-07-13). Triaging the
   * parked accepted decisions mined from brain/ledger used to mean CLI grep over
   * ~28k rows + raw system verbs — a standing-legibility-law violation. This
   * bespoke owner turns each parked decision candidate into a plain-language
   * card whose FOUR buttons never spell a raw verdict: "Make it law" / "Keep
   * aside" / "Discard" / "Decline — stop asking". The raw verb
   * (promote/park/reject/no_promote) surfaces ONLY in the disclosure argv line
   * (dual-encode). Mechanism mirrors memory-adjudicate/job-health exactly: same
   * generic AdjudicationCard, same /api/decision/execute seam, same
   * `memory adjudicate` allowlist — a new REGISTRY entry is all it takes.
   *
   * source "orient" WITHOUT requiresResolvedItem: like panel-apply, argv is
   * built purely from itemId + option, so executeDecision skips item resolution
   * (no 404) instead of re-projecting. The argv is the EXACT bare pair
   * `memory adjudicate <id> --verdict <verdict>` — no --basis: these candidates
   * already carry promotion_basis (decision_record / explicit_confirmation /
   * runtime_proof), so the memory authority accepts a bare promote. buildArgv
   * refuses any verdict outside the four; the execute path re-checks via
   * optionsFor as defense in depth. The adjudicate CLI is unchanged.
   * -------------------------------------------------------------------- */
  "standing-law-candidate": {
    area: "standing-law-candidate",
    title: "Standing-law candidate",
    glyph: "L",
    accent: "violet",
    source: "orient",
    argvAllowlist: [["memory", "adjudicate"]],
    idPattern: /^cand_[A-Za-z0-9_-]+$/,
    options: [],
    optionsFor: (item) => {
      const id = str(item.id);
      return STANDING_LAW_CHOICES.map((choice) => ({
        ...choice,
        argv: ["memory", "adjudicate", id, "--verdict", choice.value],
      }));
    },
    defaultOption: "park",
    plainLanguage: (item) => {
      const qualifiers = (item.qualifiers ?? {}) as Json;
      const chips: { label: string; tone: Accent }[] = [];
      if (str(qualifiers.lifecycle_status)) chips.push({ label: str(qualifiers.lifecycle_status), tone: "green" });
      if (str(qualifiers.kind)) chips.push({ label: str(qualifiers.kind), tone: "violet" });
      return {
        // THE CLAIM — the decision's own words. No id in the headline.
        headline: shortText(str(item.object) || str(item.entity), 160),
        // WHERE FROM — humanized provenance, not a system id.
        body: standingLawProvenance(item),
        evidence: [str(qualifiers.decision_id), str(qualifiers.source)].filter(Boolean),
        chips,
        consequences: {
          approve:
            `${memoryChoice("promote", "rule").label}: ${memoryChoice("promote", "rule").means}`,
          hold: "It stays on file here; agents are not told it until you choose.",
        },
      };
    },
    fields: [],
    buildArgv: (ctx) => {
      if (!STANDING_LAW_CHOICES.some((choice) => choice.value === ctx.option)) return null;
      return ["memory", "adjudicate", ctx.itemId, "--verdict", ctx.option];
    },
    previewArgv: (item) => ["memory", "adjudicate", str(item.id), "--verdict", "park"],
  },

  "panel-apply": {
    area: "panel-apply",
    title: "Apply panel run",
    glyph: "P",
    accent: "violet",
    // No projection item backs a run id; the id itself (validated below) is
    // the whole input. `source: "orient"` without `requiresResolvedItem`
    // lets executeDecision skip item resolution instead of 404ing.
    source: "orient",
    requiresConfirmation: true,
    // Real CLI shape (verified 2026-07-08): `panel run --plan <run_id> --apply`.
    // There is no `panel apply` subcommand. argv is built server-side below;
    // the allowlist prefix is safe because buildArgv always appends --apply.
    argvAllowlist: [["panel", "run"]],
    idPattern: /^prun_[A-Za-z0-9_]+$/,
    plainLanguage: () => ({
      headline: "Apply this panel run",
      body: "Records every verdict in the run under one operator act; each applied target still gets its own PolicyDecision.",
      consequences: {
        approve: "the run's verdicts are recorded (one PolicyDecision per target); the pile clears from Needs You",
        hold: "the run stays unapplied; verdicts remain individually reviewable",
      },
    }),
    options: [
      {
        value: "apply",
        label: "Apply",
        tone: "green",
        description: "Record every verdict in this panel run.",
      },
    ],
    defaultOption: "apply",
    fields: [],
    buildArgv: (ctx) => ["panel", "run", "--plan", ctx.itemId, "--apply"],
  },
  "panel-verdict-record-group": {
    area: "panel-verdict-record-group",
    title: "Record panel reviews",
    glyph: "P",
    accent: "violet",
    source: "orient",
    requiresConfirmation: true,
    effectKind: "record",
    argvAllowlist: [["panel", "verdict-record"]],
    idPattern: /^panel-verdicts\|prun_[A-Za-z0-9_]+\|(needs_human|needs_trial)$/,
    plainLanguage: () => ({
      headline: "Record grouped panel reviews",
      body: "Writes human-review sidecars for every member in the group. It does not apply panel verdicts or adjudicate memory.",
      consequences: {
        approve: "member review records are written; follow-up apply/adjudication remains explicit",
        hold: "the group stays in Needs You",
      },
    }),
    options: [
      {
        value: "record",
        label: "Record",
        tone: "green",
        description: "Record human review sidecars for this group.",
      },
    ],
    defaultOption: "record",
    fields: [],
    buildArgv: (ctx) => [
      "panel",
      "verdict-record",
      "--group",
      ctx.itemId,
      "--rationale",
      str(ctx.fields.rationale) || "operator recorded grouped panel human-review verdicts",
      "--reviewed-by",
      str(ctx.fields.reviewed_by) || "operator",
      "--json",
    ],
  },

  /* ----------------------------------------------------------------------
   * route-promotions (Phase 3b bespoke). The projection carries NO
   * actions[] for this area (verified: `reviews --json --area
   * route-promotions` → actions:null). The ONLY clean-argv mutation the
   * backend exposes is `route promotion-record --review <json> --rationale
   * <text> --json`, and `RecordPromotionHoldReview` (internal/routeevidence/
   * promotion_review_record.go) HARD-REQUIRES the review's recommendation to
   * be `hold_insufficient_evidence` — a promote/demote/hold_failing review
   * cannot be recorded here ("separate authority review required"). So this
   * spec offers exactly ONE honest one-click — "Record hold" — and only for
   * hold-insufficient reviews (optionsFor returns [] otherwise, so a
   * promote/demote item honestly falls through to copy-command; we never
   * fabricate a promote/demote argv). The rationale is a required textarea:
   * it is written verbatim into the review artifact as provenance. Recording
   * a hold applies NO authority mutation (no lane promotion/demotion) — it is
   * reversible (the `.reviewed.json` sidecar can be removed/overwritten).
   * -------------------------------------------------------------------- */
  "route-promotions": {
    area: "route-promotions",
    title: "Route promotion",
    glyph: "R",
    accent: "blue",
    source: "reviews",
    effectKind: "record",
    argvAllowlist: [["route", "promotion-record"]],
    idPattern: /^route_promotion_[a-z0-9]+$/,
    optionsFor: (item) => {
      // Recommendation is encoded in group_key: `route-promotions|<rec>|<class>`.
      const rec = routePromotionRecommendation(item);
      if (rec !== "hold_insufficient_evidence") return [];
      const path = str(item.json_path);
      return [
        {
          value: "record_hold",
          label: "Record hold",
          tone: "orange",
          description:
            "Record the operator hold as a review artifact. No lane promotion or demotion is applied.",
          argv: path
            ? ["route", "promotion-record", "--review", path, "--rationale", "…", "--json"]
            : null,
        },
      ];
    },
    options: [],
    defaultOption: "record_hold",
    plainLanguage: (item) => {
      const rec = routePromotionRecommendation(item);
      const summary = str(item.operator_summary) || "Route promotion review.";
      const lane = str(item.context_label);
      const consequences =
        rec === "hold_insufficient_evidence"
          ? {
              approve:
                "your hold is recorded as provenance (a review artifact) — no lane is promoted or demoted; the watcher keeps collecting evidence",
              hold: "nothing changes; the lane keeps its current role and stays under watch",
            }
          : {
              approve:
                "a separate route/model authority action can change this lane's role; this review card cannot promote or demote it",
              hold: "the router keeps sending work to this lane exactly as it does now",
            };
      return {
        headline: summary,
        body:
          rec === "hold_insufficient_evidence"
            ? "Not enough route evidence yet to change this lane's role. Recording a hold just marks that you reviewed it."
            : shortText(str(item.next_action), 200) ||
              "This lane's role change is a routing-authority decision — reviewed here, applied by a separate authority step.",
        evidence: arr(item.evidence_refs).map(String).slice(0, 8),
        chips: lane ? [{ label: shortText(lane, 34), tone: "blue" as Accent }] : [],
        consequences,
      };
    },
    fields: [
      {
        name: "rationale",
        type: "textarea",
        label: "Rationale",
        required: true,
        placeholder:
          "Why hold this lane? This text is written verbatim into the review artifact as the recorded provenance.",
      },
    ],
    buildArgv: (ctx) => {
      const path = str(ctx.item.json_path);
      const rationale = str(ctx.fields.rationale).trim();
      if (!path || !rationale) return null;
      return ["route", "promotion-record", "--review", path, "--rationale", rationale, "--json"];
    },
    previewArgv: (item) => {
      const path = str(item.json_path);
      return path ? ["route", "promotion-record", "--review", path, "--rationale", "…", "--json"] : null;
    },
  },

  /* ----------------------------------------------------------------------
   * synthesis-contradictions (Phase 3b bespoke). The projection carries NO
   * actions[] (verified), and the backend deliberately exposes NO clean
   * mutation for a contradiction: the synthesis sweep "may append needs_review
   * claim markers without candidate adjudication or claim promotion/
   * supersession" (cmd/compound/synthesis.go) and its group summary states
   * "each contradiction needs its own read". So this is an honest read-rich
   * card — plain-language framing of the two disagreeing claims + evidence +
   * the consequence pair — with NO executable option (options stay []). Acting
   * means opening the claim group and, only if evidence proves a supersession,
   * building a focused repair packet by hand; there is no one-click to
   * fabricate. Rendered with an honest copy-command by the Decide page.
   * -------------------------------------------------------------------- */
  "synthesis-contradictions": {
    area: "synthesis-contradictions",
    title: "Memory contradiction",
    glyph: "C",
    accent: "violet",
    source: "reviews",
    argvAllowlist: [],
    idPattern: /^synthesis_contradiction_[a-z0-9]+$/,
    options: [],
    plainLanguage: (item) => ({
      headline: str(item.operator_summary) || "Memory holds two claims that disagree.",
      body:
        shortText(str(item.next_action), 220) ||
        "Two stored claims disagree. Read the claim group; only build a repair packet if current evidence proves one supersedes the other.",
      evidence: arr(item.evidence_refs).map(String).slice(0, 12),
      chips: str(item.context_label)
        ? [{ label: shortText(str(item.context_label), 34), tone: "violet" as Accent }]
        : [],
      consequences: {
        approve:
          str(item.operator_unlock) ||
          "you settle which claim is true and memory stops carrying both versions — done by hand, only when evidence proves supersession",
        hold: "memory keeps both claims; agents may recall either until you rule",
      },
    }),
    fields: [],
  },

  /* ----------------------------------------------------------------------
   * source (Phase 3b bespoke — fixes /sources copy-only). The source verdict
   * CLIs (`source observation-verdict-review <review.yaml>`,
   * `observation-verdict-plan --verdict <file>`) all need a prepared file, so
   * they are not one-click. The one clean-argv, card-scoped act is
   * `source intake-work-plan --card <sicard_id> --json` (cmd/compound/
   * source.go): it builds a GOVERNED intake work plan (its own PolicyDecision)
   * for one source card — reversible (the planned work can be cancelled).
   * source:"orient" + no requiresResolvedItem means executeDecision skips item
   * resolution and builds argv from the card id itself (same shape as
   * panel-apply). Cards whose action is a pure read or is blocked on private
   * config are rendered as honest copy-command by the /sources page instead.
   * -------------------------------------------------------------------- */
  source: {
    area: "source",
    title: "Source intake",
    glyph: "S",
    accent: "blue",
    source: "orient",
    argvAllowlist: [["source", "intake-work-plan"]],
    idPattern: /^sicard_[a-z0-9]+$/,
    options: [
      {
        value: "intake_plan",
        label: "Plan intake work",
        tone: "green",
        description: "Build a governed intake work plan for this source card.",
        argv: ["source", "intake-work-plan", "--card", "…", "--json"],
      },
    ],
    defaultOption: "intake_plan",
    plainLanguage: (item) => ({
      headline: str(item.title) || str(item.label) || "Plan intake work for this source card",
      body:
        shortText(str(item.next_safe_action) || str(item.next_action), 200) ||
        "Turn this source card into a governed intake work plan. Nothing is collected, authed, or scheduled from this card.",
      consequences: {
        approve:
          "a governed intake work plan is created with its own PolicyDecision — no collector, credential, or scheduler is touched",
        hold: "the source card stays in the queue untouched",
      },
    }),
    fields: [
      { name: "actor", type: "text", label: "Actor", placeholder: "codex", defaultValue: "codex" },
    ],
    buildArgv: (ctx) => ["source", "intake-work-plan", "--card", ctx.itemId, "--json"],
  },
};

/** The five plain-language job-health choices over the Go enum
 * (internal/authority/job_health_review.go ValidateJobHealthReview) — the
 * single place decision-registry.ts spells out that enum for the console. */
const JOB_HEALTH_DECISIONS: { value: string; label: string; tone: Accent; description: string }[] = [
  { value: "repair_required", label: "Repair it", tone: "orange", description: "File repair work for this job." },
  { value: "parked", label: "Park it", tone: "gray", description: "Revisit when the job matters again." },
  { value: "disabled", label: "Disable it", tone: "pink", description: "Confirm the job is intentionally off." },
  { value: "no_action", label: "No action needed", tone: "green", description: "Nothing to do right now." },
  { value: "owner_action_done", label: "Already handled", tone: "blue", description: "Verify the owner's fix on the next run." },
];

/** The four plain-language standing-law choices. Each `value` is the raw memory
 * verdict (used to build argv + validate server-side); the `label` is the plain
 * copy the operator reads — the two are deliberately different so no button ever
 * spells a system verb (dual-encode: plain up front, grammar in the disclosure
 * argv). Consequence copy lives in `description`, matching the operator brief. */
const STANDING_LAW_CHOICES: { value: string; label: string; tone: Accent; description: string }[] = [
  ...(["promote", "park", "reject", "no_promote"] as const).map((value) => ({
    value,
    // One wording owner (lib/plain-words.ts memoryChoice): Decide and Memory say the same thing.
    label: memoryChoice(value, "rule").label,
    tone: ({ promote: "green", park: "orange", reject: "red", no_promote: "gray" } as const)[value],
    description: memoryChoice(value, "rule").means,
  })),
];

const PROVENANCE_MONTHS = [
  "January", "February", "March", "April", "May", "June",
  "July", "August", "September", "October", "November", "December",
];

/** "2026-06-30T14:20:51Z" → "June 30" (UTC, matching the stored Z stamp). */
function provenanceMonthDay(iso: string): string {
  const d = new Date(iso);
  if (!Number.isFinite(d.getTime())) return "";
  return `${PROVENANCE_MONTHS[d.getUTCMonth()]} ${d.getUTCDate()}`;
}

/** Humanized "where from" line for a standing-law candidate — source + date in
 * plain English, never a system id. Falls back through promotion_basis when the
 * candidate carries no source (e.g. ledger-native decisions). */
function standingLawProvenance(item: Json): string {
  const qualifiers = (item.qualifiers ?? {}) as Json;
  const source = str(qualifiers.source);
  const basis = str(item.promotion_basis);
  let origin: string;
  if (source === "brain") origin = "Mined from your brain pages";
  else if (source === "brain-ledger") origin = "Mined from your decision ledger";
  else if (basis === "runtime_proof") origin = "Recorded from a runtime proof";
  else if (basis === "explicit_confirmation") origin = "From a decision you confirmed";
  else origin = "From a recorded decision";
  const when = provenanceMonthDay(str(item.created_at));
  return when ? `${origin}, ${when}.` : `${origin}.`;
}

const SOURCE_CONTROL_DECISIONS: DecisionOption[] = [
  { value: "owner_action_required", label: "Tidy it", tone: "orange", description: "Record that an owner must classify and tidy the changes. No files change now." },
  { value: "owner_action_done", label: "Already tidied", tone: "blue", description: "Record that the owner already handled the changes; verify with the next snapshot." },
  { value: "parked", label: "Park it", tone: "gray", description: "Keep the dirty changes intentionally parked and revisit later." },
  { value: "no_action", label: "Leave as-is", tone: "green", description: "Record that no cleanup action is needed for this snapshot." },
];

function sourceControlReviewYAMLPath(item: Json): string {
  const actionPath = arr(arr(item.actions)[0]?.argv).map(String).find((part) => part.endsWith(".review.yaml"));
  return actionPath ?? arr(item.extra_paths).map(String).find((path) => path.endsWith(".review.yaml")) ?? "";
}

/** Read the job-health-review draft YAML path off a queue item's
 * `extra_paths` (server-verified live shape: `extra_paths[0]` —
 * internal/authority/job_health_review.go jobHealthReviewYAMLPath mirrors
 * this same suffix search server-side). Searching by suffix rather than
 * indexing [0] survives extra_paths gaining or reordering sibling paths. */
function jobHealthReviewYAMLPath(item: Json): string {
  return arr(item.extra_paths).map(String).find((path) => path.endsWith(".review.yaml")) ?? "";
}

/** Recommendation for a route-promotion review, read from its `group_key`
 * (`route-promotions|<recommendation>|<class>`), falling back to an explicit
 * field. Drives whether the only clean act ("record hold") is offered. */
function routePromotionRecommendation(item: Json): string {
  const fromKey = str(item.group_key).split("|")[1];
  return (fromKey || str(item.recommendation)).trim();
}

/** Read the auto-admit class from a synthesized grant-mint item's decision
 * card meta (Go owner: internal/decisions/grant_mint.go). Falls back to the
 * only currently enabled class so the card stays actionable if meta is thin. */
function grantMintClass(item: Json): string {
  const card = item.decision_card && typeof item.decision_card === "object" ? (item.decision_card as Json) : {};
  const meta = card.meta && typeof card.meta === "object" ? (card.meta as Json) : {};
  return str(meta.auto_admit_class) || "job_health";
}

/** Waiting-item count from the grant-mint decision card meta; clamped to >= 0. */
function grantMintWaitingCount(item: Json): number {
  const card = item.decision_card && typeof item.decision_card === "object" ? (item.decision_card as Json) : {};
  const meta = card.meta && typeof card.meta === "object" ? (card.meta as Json) : {};
  const n = Math.trunc(Number(meta.waiting_count));
  return Number.isFinite(n) && n > 0 ? n : 0;
}

/** Parse an integer threshold field. Empty ⇒ the field's default; a non-integer
 * ⇒ null (buildArgv rejects, so the CLI is never handed a malformed threshold). */
function grantMintInt(raw: unknown, fallback: number): number | null {
  const s = String(raw ?? "").trim();
  if (s === "") return fallback;
  if (!/^-?\d+$/.test(s)) return null;
  const n = Number(s);
  return Number.isSafeInteger(n) ? n : null;
}

/** Look up a spec by area. */
export function specFor(area: string): DecisionAreaSpec | null {
  return REGISTRY[area] ?? null;
}

/** Extract the serializable subset for the client form. */
export function toFormSpec(spec: DecisionAreaSpec): DecisionFormSpec {
  return {
    area: spec.area,
    options: spec.options,
    fields: spec.fields,
    defaultOption: spec.defaultOption,
  };
}

/** All specs sourcing from a given projection. */
export function specsForSource(source: DecisionAreaSpec["source"]): DecisionAreaSpec[] {
  return Object.values(REGISTRY).filter((spec) => spec.source === source);
}

/* ---------- what a yes and a no would cause, per kind ---------- */

/**
 * The real question behind each kind of decision, and what a yes and a no
 * would cause in the world, in his words. This table is the one owner of
 * those words: cards read them, components never write their own. `null`
 * means the records of that kind do not say, and the card says so once.
 * Only what the owner tools really do today is written here; when a yes
 * cannot run yet, the sentence says that instead of promising it.
 */
export type DecisionKindWords = {
  /** the question, in one plain sentence */
  question: string;
  /** what a yes would cause */
  yes: string | null;
  /** what a no would cause */
  no: string | null;
  /** whether it can be undone */
  undo: string | null;
  /** each item inside is decided on its own (held work) */
  perItem?: boolean;
  /** the part of his setup it is about, when the record names no app */
  about?: string;
  /** what one item is called ("proposed memory"), so a count says what it counts */
  unit?: readonly [one: string, many: string];
  /** where he answers it, when another page already owns the choice */
  answerAt?: { label: string; href: string };
};

const MEMORY_YES = memoryChoice("promote", "memory");
const MEMORY_PARK = memoryChoice("park", "memory");
const MEMORY_REJECT = memoryChoice("reject", "memory");

const KIND_WORDS: Record<string, DecisionKindWords> = {
  "workbench-blocked-on-you": {
    question: "Let this held work continue, or stop it?",
    yes: "It goes back to ready, and an agent picks it up again.",
    no: "It is closed as cancelled. Nothing is deleted; its record stays.",
    undo: "A yes can be undone by holding it again. A no cannot: a cancelled item stays closed.",
    perItem: true,
    unit: ["held work item", "held work items"],
  },
  "decision-candidates": {
    question: "Make this a standing rule for agents?",
    yes: "Today a yes only starts an independent review of the rule. No step turns it into a rule yet.",
    // Derived: a no on a proposed rule keeps things as they are.
    no: "Nothing changes: agents keep working as they do now, and the rule is not made.",
    undo: null,
    about: "Every agent (a rule all of them follow)",
  },
  "product-hypotheses": {
    question: "Approve this idea so agents can test it?",
    yes: "The idea is recorded as approved, so agents can build the change that tests it.",
    no: "The idea is not approved, so no agent builds or tests the change. It stays on your list until an agent records your no.",
    undo: null,
  },
  "product-reviews": {
    question: "Did this change work?",
    yes: "The result is recorded as worked, and the idea it tested counts as supported.",
    no: "The result is recorded as did not work, and the idea counts as refuted.",
    undo: null,
  },
  "product-results-due": {
    question: "Did this change work?",
    yes: "The result is recorded as worked, and the idea it tested counts as supported.",
    no: "The result is recorded as did not work, and the idea counts as refuted. You can also say it is unclear.",
    undo: null,
    unit: ["change to judge", "changes to judge"],
  },
  "memory-proposals": {
    question: "Should agents be told what these proposed memories say?",
    yes: `${MEMORY_YES.label}: ${MEMORY_YES.means}`,
    no: `${MEMORY_PARK.label}: ${MEMORY_PARK.means} Or ${MEMORY_REJECT.label.toLowerCase()}: ${MEMORY_REJECT.means}`,
    undo: null,
    about: "What agents know",
    unit: ["proposed memory", "proposed memories"],
    answerAt: { label: "Judge them on the Memory page", href: memoryReviewHref() },
  },
  "hook-approval-requests": {
    question: "Let the agent go ahead with this held action?",
    yes: "The agent runs the action while its approval window is open.",
    no: "The agent does not run it; it waits or takes another way.",
    undo: null,
    about: "An agent's action, held for you",
    unit: ["held action", "held actions"],
  },
  "data-source-setup": {
    question: "Give this data source the private setup it needs?",
    yes: "Once you add the account or key it names, the source reports again. Agents cannot add private setup for you.",
    no: "The source stays out of date, and its numbers stop changing.",
    undo: null,
    about: "Your data sources",
    unit: ["data source", "data sources"],
  },
  "route-promotions": {
    question: "Let agents use this model first for this kind of task?",
    yes: "Agents try this model first for this kind of task. A yes needs a permission that is not installed yet, so it cannot run today.",
    no: "Nothing changes: the current first choice stays, and your no is recorded.",
    undo: "Yes: the first choice can be switched back.",
    about: "Which model agents try first",
  },
  "harness-edit-proposals": {
    question: "Let agents make these changes to their own setup?",
    yes: "Each change you allow is approved; an agent still has to make it.",
    no: "The change is turned down and not made.",
    undo: "Yes: nothing changes until an agent makes it.",
    about: "How agents find their tools",
  },
  "product-content-queue": {
    question: "Approve these drafts for posting?",
    yes: "The drafts go into the posting queue.",
    no: "The drafts are not posted. They stay on your list until an agent records your no.",
    undo: null,
  },
  "worktree-retirement": {
    question: "Remove this finished copy of the code and its branch?",
    yes: "The copy and its branch are removed.",
    no: "They stay as they are.",
    undo: "No.",
    about: "Copies of code on this machine",
  },
  "git-index-lock": {
    question: "Clear the stale lock file in this repository?",
    yes: "The stale lock file is removed, so git works there again.",
    no: "The lock stays, and git stays stuck there.",
    undo: null,
  },
  "launchd-repair": {
    question: "Apply the repair plan to this background job?",
    yes: "The job is changed as the plan says.",
    no: "Nothing changes: the background job stays as it is.",
    undo: null,
    about: "A background job on this machine",
  },
  "source-control-cleanup": {
    question: "Tidy the uncommitted files in this repository?",
    yes: null,
    no: "Nothing changes: the files stay as they are.",
    undo: null,
  },
};

/**
 * A recommendation names one of the record's own options. Codes that are not
 * a plain yes or no are said in words here, never shown as the code; an
 * unknown code is left out and only the reason is shown.
 */
const RECOMMENDED_OPTION_WORDS: Record<string, string> = {
  resolve_reviewed: "Close it: the record says this is already done, so nothing is left to run",
  approve: "Say yes",
  promote: "Say yes",
  accept: "Say yes",
  reject: "Say no",
  dismiss: "Say no",
  park: "Not now",
  defer: "Not now",
};

/** A recommended option in words ("" for a code with no plain words). */
export function recommendedOptionWords(option: string): string {
  return RECOMMENDED_OPTION_WORDS[(option || "").trim().toLowerCase()] ?? "";
}

/** Recommended options that mean the thing is already done: a yes or no would change nothing. */
export function recommendationSaysDone(option: string): boolean {
  return (option || "").trim().toLowerCase() === "resolve_reviewed";
}

/** The question and its consequences for one kind (area id or area path); null for a kind with no entry. */
export function decisionKindWords(area: string): DecisionKindWords | null {
  return KIND_WORDS[(area || "").split("/")[0]] ?? null;
}
