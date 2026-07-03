import { readFile, writeFile } from "node:fs/promises";

import { arr, type Json, str } from "@/lib/compound";
import { shortText, targetLabel, uniqueBoundaries } from "@/lib/plain-language";
import { externalReviewYamlPath } from "@/lib/review-data";
import { replaceReviewDecisionFields, type ReviewDecision } from "@/lib/review-yaml";

/*
 * The Decision Registry — the heart of the console-as-control-surface.
 *
 * Each entry declares how a decision AREA renders in plain language and how a
 * one-click approval maps to a `compound` CLI invocation. The keystone: most
 * review items already carry `actions[0].argv` in their projection, so the CLI
 * command is read from the (server-resolved) projection rather than hardcoded —
 * the registry only adds the human layer (headline/options/fields) plus any
 * pre-execution step (e.g. rewriting an intake YAML).
 *
 * Adding a new area = adding one entry here; the generic DecisionCard,
 * DecisionForm, and /api/decision/execute route pick it up automatically.
 */

/* ---------- types ---------- */

export type Accent = "blue" | "green" | "orange" | "pink" | "violet" | "red" | "gray";

export type FieldType = "text" | "textarea" | "number" | "select";

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
 * server→client boundary into the interactive form (functions/preExec/fs stay
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
};

export type ExecContext = {
  itemId: string;
  item: Json;
  option: string;
  fields: Record<string, string>;
};

export type DecisionAreaSpec = {
  area: string;
  title: string;
  glyph: string;
  accent: Accent;
  source: "reviews" | "workbench" | "orient";

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

  /** Optional pre-execution mutation (e.g. rewrite an intake review YAML). */
  preExec?: (ctx: ExecContext) => Promise<void>;

  /** Resolve a fresh argv preview for the caption (client-side, display only). */
  previewArgv?: (item: Json) => string[] | null;
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
  admitted: { label: "Admit", tone: "green", description: "Accept into the active queue." },
  ready: { label: "Mark ready", tone: "blue", description: "Context + policy available; ready to claim." },
  claimed: { label: "Claim", tone: "violet", description: "Operator takes the work." },
  running: { label: "Start running", tone: "blue", description: "Begin execution." },
  verifying: { label: "Move to verifying", tone: "blue", description: "Hand off to proof-check." },
  blocked: { label: "Mark blocked", tone: "orange", description: "Waiting on a dependency." },
  held: { label: "Hold", tone: "orange", description: "Park for now; resumable." },
  completed: { label: "Complete", tone: "green", description: "Done with proof; closes the work." },
  failed: { label: "Mark failed", tone: "red", description: "Did not succeed; closes the work." },
  cancelled: { label: "Cancel", tone: "gray", description: "Drop the work; audit-only close." },
};

export const MOVE_TYPES = [
  { value: "outward_delta", label: "Outward delta" },
  { value: "memory_delta", label: "Memory delta" },
  { value: "rule_delta", label: "Rule delta" },
  { value: "landed_owner", label: "Landed owner" },
  { value: "control_plane_repair", label: "Control-plane repair" },
  { value: "audit_only", label: "Audit only" },
];

/** Build the work argv for a target state. Terminal targets use `work close`. */
export function workArgvFor(
  workId: string,
  target: string,
  summary: string,
  moveType: string,
): string[] {
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
  return (WORK_TRANSITIONS[state] ?? []).map((target) => {
    const meta = WORK_OPTION_META[target] ?? {
      label: target,
      tone: "gray" as Accent,
      description: target,
    };
    return {
      value: target,
      label: meta.label,
      tone: meta.tone,
      description: meta.description,
      argv: workArgvFor(id, target, "", "control_plane_repair"),
    };
  });
}

/* ---------- the registry ---------- */

export const REGISTRY: Record<string, DecisionAreaSpec> = {
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
    preExec: async (ctx) => {
      // Repairs the known confidence:2.00 draft bug by clamping server-side.
      const yamlPath = externalReviewYamlPath(ctx.itemId);
      const decision = ctx.option as ReviewDecision;
      const rationale = str(ctx.fields.rationale).trim();
      const confidence = clamp01(ctx.fields.confidence);
      const source = await readFile(yamlPath, "utf8");
      const updated = replaceReviewDecisionFields(source, { decision, rationale, confidence });
      await writeFile(yamlPath, updated, "utf8");
    },
  },

  "work-advance": {
    area: "work-advance",
    title: "Advance work",
    glyph: "W",
    accent: "blue",
    source: "workbench",
    argvAllowlist: [["work", "advance"], ["work", "close"], ["work", "claim"]],
    idPattern: /^work_[a-z0-9]{6,}$/,
    optionsFor: workOptionsFor,
    options: [],
    plainLanguage: (item) => {
      const id = str(item.work_id) || str(item.id);
      const surface = str(item.canonical_surface);
      const title = surface.startsWith("packet:")
        ? surface.slice("packet:".length).replace(/-/g, " ")
        : str(item.success_predicate) || id;
      const claimer = str(item.claimed_by) || str(item.owner);
      return {
        headline: title,
        body: shortText(str(item.next_action) || str(item.desired_outcome), 200),
        chips: claimer ? [{ label: claimer, tone: "violet" as Accent }] : [],
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

  "memory-adjudicate": {
    area: "memory-adjudicate",
    title: "Memory adjudication",
    glyph: "M",
    accent: "violet",
    source: "orient",
    argvAllowlist: [["memory", "adjudicate"]],
    idPattern: /^cand_[A-Za-z0-9_-]+$/,
    options: [],
    optionsFor: (item) => {
      const id = str(item.id);
      const defs: DecisionOption[] = [
        {
          value: "park",
          label: "Park",
          tone: "orange",
          description: "Keep as evidence for later. It will not enter ambient recall.",
        },
        {
          value: "no_promote",
          label: "Do not promote",
          tone: "gray",
          description: "Keep the candidate, but block promotion from this source.",
        },
        {
          value: "reject",
          label: "Reject",
          tone: "red",
          description: "Mark the claim wrong or noisy so it is not reused.",
        },
        {
          value: "promote",
          label: "Promote",
          tone: "green",
          description: "Accept as durable memory. Pick the evidence basis below.",
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
      };
    },
    defaultOption: "park",
    fields: [
      {
        name: "basis",
        type: "select",
        label: "Promotion reason",
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
};

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
