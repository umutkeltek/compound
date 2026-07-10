import { arr, num, reviewCommand, str, type Json } from "@/lib/compound";
import { ago, toDate } from "@/lib/time";
import type { AttentionReport } from "@/lib/attention";
import { type DecisionEffectKind, type DecisionOption, type FieldSpec, specFor } from "@/lib/decision-registry";
import { genericActionEffectKind, genericActionProjectionEligible, genericActionRevision, RATIONALE_REQUIRED_AREAS } from "@/lib/generic-actions";
import { areaPlain } from "@/lib/glossary";
import { areaOf } from "@/lib/review-data";

/**
 * The Decide-page view model (Phase 3b). Turns the reviews needs-you groups
 * (already ordered by the ONE `compound attention --json` projection) into a
 * flat, SERIALIZABLE list the client `DecideQueue` renders. All the judgment
 * — which act a group actually supports, in what batch mode — is resolved HERE
 * on the server, by capability, never by hardcoding an area. The client only
 * renders and governs the click.
 *
 * The honest law behind the act union: a card advertises exactly the act its
 * backend can perform, and says so in plain language.
 *   - bespoke        one registry-declared act for one item (route-promotions
 *                    hold, memory adjudication…), governed via ConfirmDialog.
 *   - batch-atomic   one run/group-scoped act that applies to every member
 *                    together; members are listed READ-ONLY, never with fake
 *                    per-member checkboxes (today's panel-verdicts).
 *   - batch-enumerable  every member carries its own clean per-member argv, so
 *                    the operator may exclude members and "Apply N of M". Lights
 *                    up automatically the moment the projection emits per-member
 *                    actions (Go panel per-member ticket) — no area hardcoded.
 *   - generic        a non-bespoke area whose projection action passes the
 *                    gates; executed by actionId (Sonnet generic pathway); on a
 *                    403 the client falls back to the copy command.
 *   - copy           no safe one-click exists yet — the honest escape hatch.
 */

export type DecideDomain = "work" | "memory" | "capability" | "route" | "product" | "neutral";

/** Area → domain hue (accent only: glyph tile + dot; the needs-you seam is amber). */
const DOMAIN_BY_AREA: Record<string, DecideDomain> = {
  "route-promotions": "route",
  "route-remorse": "route",
  "synthesis-contradictions": "memory",
  "synthesis-sweeps": "memory",
  "seed-corroborations": "memory",
  "panel-verdicts": "memory",
  "memory-adjudicate": "memory",
  "distiller-improvements": "memory",
  "distiller-requests": "memory",
  "capability-lifecycle-proposals": "capability",
  "capability-evaluation-intake": "capability",
  "external-repo-intake": "capability",
  bench: "capability",
  parity: "capability",
  source: "capability",
  "source-observation-verdicts": "capability",
  "source-control-cleanup": "capability",
  "observation-candidates": "capability",
  "product-action-cards": "product",
  "job-health": "work",
  "work-advance": "work",
  "workbench-needs-claim": "work",
};

export function domainForArea(area: string): DecideDomain {
  return DOMAIN_BY_AREA[area] ?? "neutral";
}

export type DecideMember = {
  id: string;
  label: string;
  /** Clean per-member argv (without leading "compound"); null when run-scoped. */
  argv: string[] | null;
  /** Per-member action id for the generic pathway; "" when none. */
  actionId: string;
  expectedRevision: string;
};

export type DecideAct =
  | {
      mode: "bespoke";
      area: string;
      itemId: string;
      option: string;
      options: DecisionOption[];
      defaultOption: string;
      label: string;
      requiresConfirmation: boolean;
      argv: string[] | null;
      policyNote: string;
      fields: FieldSpec[];
      expectedRevision: string;
      supportsPreview: boolean;
      effectKind: DecisionEffectKind;
    }
  | {
      mode: "batch-atomic";
      area: string;
      itemId: string;
      option: string;
      label: string;
      fields: Record<string, string>;
      argv: string[] | null;
      policyNote: string;
      requiresConfirmation: boolean;
      memberCount: number;
    }
  | {
      mode: "batch-enumerable";
      area: string;
      label: string;
      members: DecideMember[];
      requiresConfirmation: boolean;
      requiresRationale: boolean;
      effectKind: DecisionEffectKind;
      policyNote: string;
    }
  | {
      mode: "generic";
      area: string;
      itemId: string;
      actionId: string;
      expectedRevision: string;
      label: string;
      argv: string[] | null;
      requiresConfirmation: boolean;
      copyCommand: string;
      effectKind: DecisionEffectKind;
      /** Opt-in only for the fixed generic review-resolution adapter. */
      reasonRequired?: boolean;
    }
  | { mode: "copy"; command: string; reason: string };

export type DecideChip = { label: string; tone: string };

export type DecideEvidence = {
  ref: string;
  label: string;
  sourceKind: string;
  summary: string;
  openCommand: string;
};

export type DecideCapability = {
  kind: "apply" | "record" | "track" | "unavailable";
  label: string;
  detail: string;
};

type GenericDecideAct = Extract<DecideAct, { mode: "generic" }>;

// Generic resolution controls are derived at render time so the serialized
// DecideGroup remains byte-identical for every existing card. Only honest-copy
// cards qualify; an already-actionable bespoke/generic/batch card gets none.
// The synthetic acts execute through the fixed server-side review-resolution
// adapter (argv is rebuilt server-side), so they carry no projection revision.
export function genericReviewResolutionActs(group: DecideGroup): GenericDecideAct[] {
  if (group.act.mode !== "copy") return [];
  const copyCommand = group.act.command;
  const make = (decision: "parked" | "dismissed", actionId: string, label: string): GenericDecideAct => ({
    mode: "generic",
    area: group.area,
    itemId: group.sampleId,
    actionId,
    expectedRevision: "",
    label,
    argv: [
      "reviews",
      "resolve",
      "--item",
      group.sampleId,
      "--decision",
      decision,
      "--reason",
      "…",
      "--json",
    ],
    requiresConfirmation: true,
    copyCommand,
    effectKind: "record",
    reasonRequired: true,
  });
  return [
    make("parked", "review_resolution_park", "Park"),
    make("dismissed", "review_resolution_dismiss", "Dismiss"),
  ];
}

export type DecideBriefFinding = { finding: string; evidenceRef: string };

export type DecideBriefOption = {
  value: string;
  label: string;
  consequenceApprove: string;
  consequenceHold: string;
  /** Copy/display material ONLY in this slice — never wired into /api/decision/execute. */
  commandArgv: string[] | null;
};

export type DecideBriefRecommendation = { option: string; confidence: number; rationale: string };

/** compound.decision_brief.v0.1 (internal/reviewartifact/brief.go), mapped to camelCase for the client. */
export type DecideBrief = {
  headline: string;
  whatThisIs: string;
  findings: DecideBriefFinding[];
  options: DecideBriefOption[];
  recommendation: DecideBriefRecommendation;
  preparedBy: string;
  preparedAt: string;
};

export type DecideGroup = {
  key: string;
  area: string;
  areaLabel: string;
  areaPlain: string;
  domain: DecideDomain;
  glyph: string;
  title: string;
  unlock: string;
  body: string;
  consequences: { approve: string; hold: string } | null;
  chips: DecideChip[];
  count: number;
  createdAt: string;
  ageLabel: string;
  old: boolean;
  sampleId: string;
  members: DecideMember[];
  evidence: DecideEvidence[];
  freshness: { generatedAt: string; source: string };
  act: DecideAct;
  capability: DecideCapability;
  /**
   * A prepared, propose-only translation of this decision — optional so
   * existing DecideGroup literals built outside buildDecideGroups (e.g. the
   * external-repo-intake card in app/reviews/page.tsx) keep compiling
   * unchanged. undefined/null means "render exactly as before this packet."
   */
  brief?: DecideBrief | null;
};

/* ---------- helpers ---------- */

function cleanArgv(rawArgv: unknown): string[] {
  const argv = arr(rawArgv).map(String).filter(Boolean);
  if (!argv.length) return [];
  return argv[0] === "compound" ? argv.slice(1) : argv;
}

/** Is this argv a clean `compound …` invocation (auto-excludes /bin/sh forms)? */
function isCleanCompound(rawArgv: unknown): boolean {
  const argv = arr(rawArgv).map(String);
  return argv.length >= 2 && argv[0] === "compound";
}

function safeBatchAction(group: Json): Json | null {
  const action = group.safe_batch_action;
  return action && typeof action === "object" ? (action as Json) : null;
}

function actionFlag(action: Json, flag: string): string {
  const argv = arr(action.argv).map(String);
  const index = argv.indexOf(flag);
  return index >= 0 ? str(argv[index + 1]) : "";
}

const RUN_ID_RE = /^prun_[A-Za-z0-9_]+$/;

function panelApplyRunId(action: Json): string {
  if (str(action.id) !== "panel_run_apply") return "";
  const argv = cleanArgv(action.argv);
  if (argv.length !== 5 || argv[0] !== "panel" || argv[1] !== "run" || argv[2] !== "--plan" || argv[4] !== "--apply") {
    return "";
  }
  return RUN_ID_RE.test(argv[3] ?? "") ? argv[3] : "";
}

function memberOf(area: string, id: string, itemByID: Map<string, Json>): DecideMember {
  const item = itemByID.get(id);
  const action = item
    ? arr(item.actions).find((candidate) => genericActionProjectionEligible(area, (candidate as Json).argv))
    : null;
  const argv = action ? cleanArgv((action as Json).argv) : null;
  return {
    id,
    label: str(item?.context_label) || str(item?.operator_summary) || id,
    argv,
    actionId: action ? str((action as Json).id) : "",
    expectedRevision: action ? genericActionRevision(action) : "",
  };
}

/**
 * Capability probe: enumerable ONLY when EVERY member carries its own clean
 * per-member argv (the projection's per-member action). Returns the member
 * list when enumerable, else null (→ the group stays honestly atomic-run).
 * This is the single detection point that lights up the enumerable batch UI
 * when the Go panel per-member ticket lands — no area is hardcoded.
 */
function enumerableMembers(group: Json, itemByID: Map<string, Json>): DecideMember[] | null {
  const ids = arr(group.item_ids).map(String).filter(Boolean);
  if (ids.length < 1) return null;
  const area = areaOf(group);
  const members = ids.map((id) => memberOf(area, id, itemByID));
  return members.every((m) => m.argv && m.argv.length > 0 && m.actionId) ? members : null;
}

function readonlyMembers(group: Json, itemByID: Map<string, Json>): DecideMember[] {
  const area = areaOf(group);
  return arr(group.item_ids)
    .map(String)
    .filter(Boolean)
    .map((id) => {
      const m = memberOf(area, id, itemByID);
      return { ...m, argv: null }; // read-only: strip any argv so the client can't fake exclusion
    });
}

function groupCreatedAt(group: Json, itemByID: Map<string, Json>): string {
  const sample = group.sample;
  const sampleCreated = sample && typeof sample === "object" ? str((sample as Json).created_at) : "";
  if (sampleCreated) return sampleCreated;
  let min = "";
  for (const id of arr(group.item_ids).map(String)) {
    const created = str(itemByID.get(id)?.created_at);
    if (created && (!min || created < min)) min = created;
  }
  return min || str(group.created_at);
}

function isOldAge(createdAt: string): boolean {
  const d = toDate(createdAt);
  return d ? Date.now() - d.getTime() > 48 * 3600 * 1000 : false;
}

/** Preserve the owner-projected evidence body; labels alone erase judgment context. */
function groupEvidence(group: Json, fallback: string[]): DecideEvidence[] {
  const summaries = arr(group.evidence_summaries).filter((s): s is Json => Boolean(s) && typeof s === "object");
  if (summaries.length) {
    return summaries
      .map((s) => ({
        ref: str(s.ref),
        label: str(s.label) || str(s.ref),
        sourceKind: str(s.source_kind),
        summary: str(s.summary),
        openCommand: str(s.open_command),
      }))
      .filter((s) => s.ref || s.label)
      .slice(0, 12);
  }
  const refs = arr(group.evidence_refs).map(String).filter(Boolean);
  return (refs.length ? refs : fallback).slice(0, 12).map((ref) => ({
    ref,
    label: ref,
    sourceKind: "reference",
    summary: "",
    openCommand: "",
  }));
}

/**
 * Maps a raw `item.brief` (compound.decision_brief.v0.1, already validated
 * server-side by internal/reviewartifact.ParseDecisionBrief) into the client
 * shape. Independently defensive: requires a headline, a prepared_by, and at
 * least one finding with an evidence ref, so a stale/partial payload can
 * never half-render even if it somehow reached the client — belt-and-
 * suspenders on top of the server-side evidence-ref-required /
 * prepared_by-required validation, not a replacement for it.
 */
function parseBrief(raw: unknown): DecideBrief | null {
  if (!raw || typeof raw !== "object") return null;
  const b = raw as Json;
  const headline = str(b.headline);
  const preparedBy = str(b.prepared_by);
  if (!headline || !preparedBy) return null;
  const findings = arr(b.findings)
    .filter((f): f is Json => Boolean(f) && typeof f === "object")
    .map((f) => ({ finding: str(f.finding), evidenceRef: str(f.evidence_ref) }))
    .filter((f) => f.evidenceRef);
  if (!findings.length) return null;
  const options = arr(b.options)
    .filter((o): o is Json => Boolean(o) && typeof o === "object")
    .map((o) => ({
      value: str(o.value),
      label: str(o.label),
      consequenceApprove: str(o.consequence_approve),
      consequenceHold: str(o.consequence_hold),
      commandArgv: Array.isArray(o.command_argv) ? arr(o.command_argv).map(String).filter(Boolean) : null,
    }));
  const rec = b.recommendation && typeof b.recommendation === "object" ? (b.recommendation as Json) : {};
  return {
    headline,
    whatThisIs: str(b.what_this_is),
    findings,
    options,
    recommendation: {
      option: str(rec.option),
      confidence: num(rec.confidence),
      rationale: str(rec.rationale),
    },
    preparedBy,
    preparedAt: str(b.prepared_at),
  };
}

/**
 * Scans EVERY group member for a valid brief — NOT just `group.sample`.
 * `group.sample` is only the FIRST member the Go projection saw while
 * building the group (internal/reviewartifact/queue.go buildQueueGroups sets
 * Sample once, on first-seen) — a brief prepared for any other member would
 * silently vanish from the card if this only looked at the sample. Verified
 * against the live proof shape: clp_326cfa93213e (capability-lifecycle-
 * proposals, 4 members) — the briefed member is not guaranteed to be first.
 */
function groupBriefTarget(group: Json, itemByID: Map<string, Json>): { brief: DecideBrief; item: Json } | null {
  for (const id of arr(group.item_ids).map(String)) {
    const item = itemByID.get(id);
    const parsed = parseBrief(item?.brief);
    if (item && parsed) return { brief: parsed, item };
  }
  const sample = group.sample;
  if (!sample || typeof sample !== "object") return null;
  const item = sample as Json;
  const brief = parseBrief(item.brief);
  return brief ? { brief, item } : null;
}

function fallbackCommand(group: Json, sample: Json): string {
  const action = safeBatchAction(group);
  if (action) {
    const command = str(action.command);
    if (command) return command;
    const argv = cleanArgv(action.argv);
    if (argv.length) return `compound ${argv.join(" ")}`;
  }
  return reviewCommand(sample);
}

/** Map a projected batch action id onto its bespoke registry area + payload. */
function batchAtomicAct(action: Json, group: Json, sample: Json): DecideAct | null {
  const argv = cleanArgv(action.argv);
  if (!isCleanCompound(action.argv) || argv.length === 0) return null;
  const memberCount = num(group.count) || arr(group.item_ids).length || 1;
  const label = str(action.label) || `Apply ${memberCount} item${memberCount === 1 ? "" : "s"}`;
  const policyNote = str(action.policy_note);
  const requiresConfirmation = action.requires_review !== false;

  const recordRationale = actionFlag(action, "--rationale");
  const recordReviewer = actionFlag(action, "--reviewed-by");
  const recordArgv = cleanArgv(action.argv);
  if (
    str(action.id) === "panel_verdict_record_group" &&
    recordArgv.length === 8 &&
    recordArgv[0] === "panel" &&
    recordArgv[1] === "verdict-record" &&
    recordArgv[2] === "--group" &&
    recordArgv[3] === str(group.key) &&
    recordArgv[4] === "--rationale" &&
    recordArgv[6] === "--reviewed-by" &&
    Boolean(recordRationale && recordReviewer)
  ) {
    return {
      mode: "batch-atomic",
      area: "panel-verdict-record-group",
      itemId: str(group.key),
      option: "record",
      label,
      fields: { rationale: recordRationale, reviewed_by: recordReviewer },
      argv,
      policyNote,
      requiresConfirmation,
      memberCount,
    };
  }

  const runId = panelApplyRunId(action);
  if (!runId) return null;
  return {
    mode: "batch-atomic",
    area: "panel-apply",
    itemId: runId,
    option: "apply",
    label,
    fields: {},
    argv,
    policyNote,
    requiresConfirmation,
    memberCount,
  };
}

/* ---------- the resolver ---------- */

function resolveAct(
  group: Json,
  sample: Json,
  sampleId: string,
  attnAction: Json | null,
  itemByID: Map<string, Json>,
): DecideAct {
  const area = areaOf(group);
  const spec = specFor(area);
  const genericEffect = genericActionEffectKind(area);

  // A) bespoke registry with a usable option for THIS item.
  if (spec) {
    const options = spec.optionsFor ? spec.optionsFor(sample) : spec.options;
    if (options.length > 0) {
      const option = spec.defaultOption ?? (options.length === 1 ? options[0].value : "");
      const chosen = options.find((o) => o.value === option);
      const argv = spec.previewArgv ? spec.previewArgv(sample) : chosen?.argv ?? null;
      return {
        mode: "bespoke",
        area,
        itemId: sampleId,
        option,
        options,
        defaultOption: option,
        label: chosen?.label ?? "Review decision",
        requiresConfirmation: true, // every governed mutation confirms
        argv,
        policyNote: "",
        fields: spec.fields,
        expectedRevision: spec.revisionFor?.(sample) ?? "",
        supportsPreview: Boolean(spec.supportsPreview),
        effectKind: spec.effectKind ?? "apply",
      };
    }
    // The bespoke owner defines the effect boundary. Zero options means it has
    // no compatible workflow for this item; a projected evidence/record action
    // cannot be substituted for a different represented effect.
    return {
      mode: "copy",
      command: reviewCommand(sample),
      reason: str(group.terminal_reason) || "The owner exposes no effect-compatible workflow for this decision.",
    };
  }

  // B) run/group-scoped batch action carried by the reviews projection.
  const sba = safeBatchAction(group);
  if (sba && isCleanCompound(sba.argv)) {
    const enumerable = genericEffect && genericActionProjectionEligible(area, sba.argv)
      ? enumerableMembers(group, itemByID)
      : null;
    if (enumerable && genericEffect) {
      const count = enumerable.length;
      return {
        mode: "batch-enumerable",
        area,
        label: str(sba.label) || `Apply ${count} of ${count}`,
        members: enumerable,
        requiresConfirmation: sba.requires_review !== false,
        requiresRationale: RATIONALE_REQUIRED_AREAS.has(area),
        effectKind: genericEffect,
        policyNote: str(sba.policy_note),
      };
    }
    const atomic = batchAtomicAct(sba, group, sample);
    if (atomic) return atomic;
  }

  // C) generic pathway — a non-bespoke area whose projection action passes the
  // gates (clean argv + an id). Executed by actionId; the client falls back to
  // the copy command on a 403 (un-allowlisted area).
  if (!spec && genericEffect && attnAction && genericActionProjectionEligible(area, attnAction.argv)) {
    const actionId = str(attnAction.id);
    const kind = str(attnAction.kind);
    const itemIDs = arr(group.item_ids).map(String).filter(Boolean);
    const targetID = kind === "batch" ? str(group.key) : itemIDs.length === 1 ? itemIDs[0] : "";
    if (actionId && targetID && (kind === "one_click" || kind === "batch")) {
      return {
        mode: "generic",
        area,
        itemId: targetID,
        actionId,
        expectedRevision: genericActionRevision(attnAction),
        label: str(attnAction.label) || "Run action",
        argv: cleanArgv(attnAction.argv),
        requiresConfirmation: Boolean(attnAction.requires_confirmation),
        copyCommand: fallbackCommand(group, sample),
        effectKind: genericEffect,
      };
    }
  }

  // D) honest copy — no safe one-click exists yet.
  return {
    mode: "copy",
    command: fallbackCommand(group, sample),
    reason: str(group.terminal_reason) || str(sba?.policy_note) || "",
  };
}

function capabilityForAct(act: DecideAct): DecideCapability {
  if (act.mode === "copy") {
    return {
      kind: "unavailable",
      label: "Owner action unavailable",
      detail: act.reason || "This owner has not exposed a safe governed workflow yet.",
    };
  }
  if (act.mode === "bespoke") {
    if (act.effectKind === "track") {
      return { kind: "track", label: "Disposition available", detail: "Choose a server-owned disposition; open work creates proposed work only." };
    }
    if (act.effectKind === "record") {
      return { kind: "record", label: "Record decision", detail: "This records reviewed provenance; it does not claim downstream success." };
    }
  }
  if ((act.mode === "batch-enumerable" || act.mode === "generic") && act.effectKind === "record") {
    return { kind: "record", label: "Record decision", detail: "This records reviewed provenance; it does not perform the downstream repair or authority change." };
  }
  if ((act.mode === "batch-enumerable" || act.mode === "generic") && act.effectKind === "track") {
    return { kind: "track", label: "Open tracked step", detail: "This creates or advances a governed plan; it does not claim downstream completion." };
  }
  if (act.mode === "batch-atomic" && act.area === "panel-verdict-record-group") {
    return { kind: "record", label: "Record decision", detail: "This records grouped review sidecars; apply remains separate." };
  }
  return { kind: "apply", label: "Apply now", detail: "A fresh owner-projected action is available behind confirmation." };
}

/* ---------- public builder ---------- */

export type SampleForGroup = (group: Json) => Json;

/** Build the serializable Decide model for the already-ordered needs-you groups. */
export function buildDecideGroups(
  orderedGroups: Json[],
  itemByID: Map<string, Json>,
  attention: AttentionReport | null,
  sampleFor: SampleForGroup,
): DecideGroup[] {
  const attnByKey = new Map<string, Json>();
  const attnByArea = new Map<string, Json>();
  for (const g of arr(attention?.groups) as Json[]) {
    const key = str(g.key);
    if (key && !attnByKey.has(key)) attnByKey.set(key, g);
    const a = str(g.area);
    if (a && !attnByArea.has(a)) attnByArea.set(a, g);
  }

  return orderedGroups.map((group) => {
    const area = areaOf(group);
    const briefTarget = groupBriefTarget(group, itemByID);
    const sample = briefTarget?.item ?? sampleFor(group);
    const sampleId = str(sample.id) || str(sample.review_id) || str(group.key);
    const spec = specFor(area);
    const pl = spec ? spec.plainLanguage(sample) : null;

    const attn = attnByKey.get(str(group.key)) ?? attnByArea.get(area) ?? null;
    const attnAction = attn && typeof attn.action === "object" ? (attn.action as Json) : null;

    const act = resolveAct(group, sample, sampleId, attnAction, itemByID);
    const createdAt = groupCreatedAt(group, itemByID);
    const count = num(group.count) || arr(group.item_ids).length || 1;

    const hasDecisionCard = Boolean(sample.decision_card && typeof sample.decision_card === "object");
    const title = (hasDecisionCard ? pl?.headline : "") || str(group.operator_summary) || pl?.headline || "Review this decision.";
    const evidence = groupEvidence(group, pl?.evidence ?? []);
    const members =
      act.mode === "batch-enumerable" ? act.members : readonlyMembers(group, itemByID);
    const brief = briefTarget?.brief ?? null;

    return {
      key: str(group.key) || sampleId,
      area,
      areaLabel: area.replace(/-/g, " "),
      areaPlain: areaPlain(area),
      domain: domainForArea(area),
      glyph: spec?.glyph || (area ? area[0].toUpperCase() : "•"),
      title,
      unlock: str(group.operator_unlock) || pl?.consequences?.approve || "",
      body: pl?.body || "",
      consequences: pl?.consequences ?? null,
      chips: (pl?.chips ?? []).map((c) => ({ label: c.label, tone: String(c.tone) })),
      count,
      createdAt,
      ageLabel: createdAt ? ago(createdAt) : "age unknown",
      old: isOldAge(createdAt),
      sampleId,
      members,
      evidence,
      freshness: {
        generatedAt: str(attention?.generated_at),
        source: "compound attention --json",
      },
      act,
      capability: capabilityForAct(act),
      brief,
    };
  });
}
