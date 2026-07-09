import { arr, num, reviewCommand, str, type Json } from "@/lib/compound";
import { ago, toDate } from "@/lib/time";
import type { AttentionReport } from "@/lib/attention";
import { type FieldSpec, specFor } from "@/lib/decision-registry";
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
};

export type DecideAct =
  | {
      mode: "bespoke";
      area: string;
      itemId: string;
      option: string;
      label: string;
      requiresConfirmation: boolean;
      argv: string[] | null;
      policyNote: string;
      fields: FieldSpec[];
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
      policyNote: string;
    }
  | {
      mode: "generic";
      area: string;
      itemId: string;
      actionId: string;
      label: string;
      argv: string[] | null;
      requiresConfirmation: boolean;
      copyCommand: string;
    }
  | { mode: "copy"; command: string; reason: string };

export type DecideChip = { label: string; tone: string };

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
  evidence: string[];
  freshness: { generatedAt: string; source: string };
  act: DecideAct;
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

function batchRunId(action: Json, group: Json, sample: Json): string {
  const argv = arr(action.argv).map(String);
  for (const flag of ["--plan", "--run"]) {
    const i = argv.indexOf(flag);
    if (i >= 0 && RUN_ID_RE.test(argv[i + 1] ?? "")) return argv[i + 1];
  }
  const keyed = str(group.key).match(/\|(prun_[A-Za-z0-9_]+)\|/)?.[1];
  if (keyed) return keyed;
  const card = sample.decision_card;
  const meta = card && typeof card === "object" ? (card as Json).meta : null;
  const fromSample = meta && typeof meta === "object" ? str((meta as Json).run_id) : "";
  return RUN_ID_RE.test(fromSample) ? fromSample : "";
}

function memberOf(id: string, itemByID: Map<string, Json>): DecideMember {
  const item = itemByID.get(id);
  const action = item ? arr(item.actions)[0] : null;
  const argv = action && isCleanCompound((action as Json).argv) ? cleanArgv((action as Json).argv) : null;
  return {
    id,
    label: str(item?.context_label) || str(item?.operator_summary) || id,
    argv,
    actionId: action ? str((action as Json).id) : "",
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
  const members = ids.map((id) => memberOf(id, itemByID));
  return members.every((m) => m.argv && m.argv.length > 0 && m.actionId) ? members : null;
}

function readonlyMembers(group: Json, itemByID: Map<string, Json>): DecideMember[] {
  return arr(group.item_ids)
    .map(String)
    .filter(Boolean)
    .map((id) => {
      const m = memberOf(id, itemByID);
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

/** Prefer readable evidence_summaries labels over raw refs. */
function groupEvidence(group: Json, fallback: string[]): string[] {
  const summaries = arr(group.evidence_summaries).filter((s): s is Json => Boolean(s) && typeof s === "object");
  if (summaries.length) return summaries.map((s) => str(s.label) || str(s.ref)).filter(Boolean).slice(0, 12);
  const refs = arr(group.evidence_refs).map(String).filter(Boolean);
  return (refs.length ? refs : fallback).slice(0, 12);
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

  if (str(action.id) === "panel_verdict_record_group") {
    return {
      mode: "batch-atomic",
      area: "panel-verdict-record-group",
      itemId: str(group.key),
      option: "record",
      label,
      fields: { rationale: actionFlag(action, "--rationale"), reviewed_by: actionFlag(action, "--reviewed-by") },
      argv,
      policyNote,
      requiresConfirmation,
      memberCount,
    };
  }

  const runId = batchRunId(action, group, sample);
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

  // A) bespoke registry with a usable option for THIS item.
  if (spec) {
    const options = spec.optionsFor ? spec.optionsFor(sample) : spec.options;
    if (options.length > 0) {
      const option = spec.defaultOption ?? options[0].value;
      const chosen = options.find((o) => o.value === option) ?? options[0];
      const argv = spec.previewArgv ? spec.previewArgv(sample) : chosen.argv ?? null;
      return {
        mode: "bespoke",
        area,
        itemId: sampleId,
        option: chosen.value,
        label: chosen.label,
        requiresConfirmation: true, // every governed mutation confirms
        argv,
        policyNote: "",
        fields: spec.fields,
      };
    }
    // spec present but no executable option (route-promotions demote,
    // synthesis-contradictions) → honest copy below, still using the spec's
    // richer plain language for the card body.
  }

  // B) run/group-scoped batch action carried by the reviews projection.
  const sba = safeBatchAction(group);
  if (sba && isCleanCompound(sba.argv)) {
    const enumerable = enumerableMembers(group, itemByID);
    if (enumerable) {
      const count = enumerable.length;
      return {
        mode: "batch-enumerable",
        area,
        label: str(sba.label) || `Apply ${count} of ${count}`,
        members: enumerable,
        requiresConfirmation: sba.requires_review !== false,
        policyNote: str(sba.policy_note),
      };
    }
    const atomic = batchAtomicAct(sba, group, sample);
    if (atomic) return atomic;
  }

  // C) generic pathway — a non-bespoke area whose projection action passes the
  // gates (clean argv + an id). Executed by actionId; the client falls back to
  // the copy command on a 403 (un-allowlisted area).
  if (!spec && attnAction && isCleanCompound(attnAction.argv)) {
    const actionId = str(attnAction.id);
    const kind = str(attnAction.kind);
    if (actionId && (kind === "one_click" || kind === "batch")) {
      return {
        mode: "generic",
        area,
        itemId: str(arr(group.item_ids).map(String)[0]) || sampleId,
        actionId,
        label: str(attnAction.label) || "Run action",
        argv: cleanArgv(attnAction.argv),
        requiresConfirmation: Boolean(attnAction.requires_confirmation),
        copyCommand: fallbackCommand(group, sample),
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
    const sample = sampleFor(group);
    const sampleId = str(sample.id) || str(sample.review_id) || str(group.key);
    const spec = specFor(area);
    const pl = spec ? spec.plainLanguage(sample) : null;

    const attn = attnByKey.get(str(group.key)) ?? attnByArea.get(area) ?? null;
    const attnAction = attn && typeof attn.action === "object" ? (attn.action as Json) : null;

    const act = resolveAct(group, sample, sampleId, attnAction, itemByID);
    const createdAt = groupCreatedAt(group, itemByID);
    const count = num(group.count) || arr(group.item_ids).length || 1;

    const title = str(group.operator_summary) || pl?.headline || "Review this decision.";
    const evidence = groupEvidence(group, pl?.evidence ?? []);
    const members =
      act.mode === "batch-enumerable" ? act.members : readonlyMembers(group, itemByID);

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
    };
  });
}
