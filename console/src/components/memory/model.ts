import { str } from "@/lib/compound";
import { specFor, type DecisionFormSpec } from "@/lib/decision-registry";
import type { MemoryCandidate } from "@/lib/memory-data";

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
};

export function buildAdjudicationModel(candidate: MemoryCandidate): AdjudicationCardModel | null {
  const spec = specFor(AREA);
  if (!spec) return null;

  const options = spec.optionsFor ? spec.optionsFor(candidate) : spec.options;
  if (!options.length) return null;

  const pl = spec.plainLanguage(candidate);
  const defaultOption = spec.defaultOption ?? options[0].value;
  const argvPreview = spec.previewArgv
    ? spec.previewArgv(candidate)
    : options.find((o) => o.value === defaultOption)?.argv ?? null;

  return {
    id: candidate.id,
    headline: pl.headline || candidate.id,
    body: pl.body,
    chips: (pl.chips ?? []).map((c) => ({ label: c.label, tone: String(c.tone) })),
    evidence: pl.evidence ?? [],
    consequences: pl.consequences ?? null,
    formSpec: { area: spec.area, options, fields: spec.fields, defaultOption },
    argvPreview,
    seed: str(candidate.entity) || candidate.id,
    createdAt: str(candidate.created_at),
  };
}
