import { str } from "@/lib/compound";
import { specFor, type DecisionFormSpec } from "@/lib/decision-registry";
import type { MemoryCandidate } from "@/lib/memory-data";
import { memoryStatement, memoryState } from "./presentation";

/*
 * Server-only view-model for the Memory adjudication queue. It CONSUMES the
 * existing `memory-adjudicate` bespoke spec (decision-registry.ts) rather than
 * re-declaring options/fields/argv — the registry owns the act; this only
 * extracts its serializable projection so a client card can render + govern the
 * click through /api/decision/execute (same payload the Decide queue posts).
 *
 * No node builtins imported here → safe from the server component; the returned
 * shape is fully serializable so it can cross into the client AdjudicationCard.
 */

const AREA = "memory-adjudicate";

export type AdjudicationCardModel = {
  id: string;
  headline: string;
  body: string;
  chips: { label: string; tone: string }[];
  evidence: string[];
  consequences: { approve: string; hold: string } | null;
  formSpec: DecisionFormSpec;
  argvPreview: string[] | null;
  /** Entity slug (or claim id) to seed the embedded memory graph from this card. */
  seed: string;
  createdAt: string;
  /** Presentation, dual-encoded per area so the same card serves the proposed
   * memory queue and the parked standing-law queue honestly (no "proposed" lie
   * on a parked candidate; no raw "promote" in the consequence lead). */
  glyph: string;
  kindLabel: string;
  stateLabel: string;
  approveLabel: string;
  fullStatement: string;
  subject: string;
  relation: string;
  sourceLabel: string;
  whyReview: string;
};

type Presentation = { kindLabel: string; approveLabel: string; defaultState: string };

/** Per-area card chrome. glyph comes from the registry spec; these are the
 * dual-encoded words the operator reads (the affirmative verb + the eyebrow). */
const PRESENTATION: Record<string, Presentation> = {
  "memory-adjudicate": { kindLabel: "Proposed memory", approveLabel: "accept for recall", defaultState: "proposed" },
  "standing-law-candidate": { kindLabel: "Proposed standing rule", approveLabel: "make it a standing rule", defaultState: "parked" },
};

export function buildAdjudicationModel(
  candidate: MemoryCandidate,
  area: string = AREA,
): AdjudicationCardModel | null {
  const spec = specFor(area);
  if (!spec) return null;

  const options = spec.optionsFor ? spec.optionsFor(candidate) : spec.options;
  if (!options.length) return null;

  const pl = spec.plainLanguage(candidate);
  const defaultOption = spec.defaultOption ?? options[0].value;
  const argvPreview = spec.previewArgv
    ? spec.previewArgv(candidate)
    : options.find((o) => o.value === defaultOption)?.argv ?? null;
  const presentation = PRESENTATION[area] ?? PRESENTATION[AREA];
  const statement = memoryStatement(candidate.object);
  const qualifiers = (candidate.qualifiers ?? {}) as Record<string, unknown>;
  const source = str(qualifiers.source);
  const sourceLabel = ({ knowledge_page: "Recorded page", knowledge_import: "Imported source", knowledge_extraction: "Extracted source", decision_ledger: "Decision record" } as Record<string, string>)[source] || (source ? "Recorded source" : "Source description unavailable");
  const refs = [...new Set([
    ...pl.evidence ?? [],
    ...(Array.isArray(candidate.evidence_refs) ? candidate.evidence_refs.filter((r): r is string => typeof r === "string") : []),
    str(qualifiers.source_ref),
  ].filter((ref) => ref && ref !== source))];

  return {
    id: candidate.id,
    headline: statement.summary,
    body: area === "standing-law-candidate" ? pl.body : "",
    chips: (pl.chips ?? []).map((c) => ({ label: c.label, tone: String(c.tone) })),
    evidence: refs,
    consequences: area === "standing-law-candidate" ? pl.consequences ?? null : {
      approve: "Agents may recall this statement as durable memory when its scope and visibility allow it.",
      hold: "It remains evidence for later and is not added to agent recall.",
    },
    formSpec: { area: spec.area, options, fields: spec.fields, defaultOption },
    argvPreview,
    seed: str(candidate.entity) || candidate.id,
    createdAt: str(candidate.created_at),
    glyph: spec.glyph,
    kindLabel: presentation.kindLabel,
    stateLabel: memoryState(str(candidate.state) || presentation.defaultState),
    approveLabel: presentation.approveLabel,
    fullStatement: statement.full,
    subject: str(candidate.entity),
    relation: str(candidate.predicate),
    sourceLabel,
    whyReview: area === "standing-law-candidate"
      ? "Decide whether this recorded decision should guide future agents as a standing rule."
      : "Decide whether future agents should rely on this statement. It has not been accepted into recall by this proposal.",
  };
}
