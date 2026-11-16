import { str } from "@/lib/compound";
import { specFor, type DecisionFormSpec } from "@/lib/decision-registry";
import { appHref, evidenceHistoryHref } from "@/lib/links";
import type { MemoryCandidate } from "@/lib/memory-data";
import { KNOWLEDGE_TERMS, memoryChoice, sentence, type MemoryChoiceFor } from "@/lib/plain-words";
import { memoryBasis, memorySource, memoryStatement, memorySubject } from "./presentation";

/*
 * Server-only view-model for the Memory review queue. It CONSUMES the
 * existing `memory-adjudicate` / `standing-law-candidate` specs
 * (decision-registry.ts) for the choices and their exact commands rather than
 * re-declaring them; the words he reads come from lib/plain-words.ts.
 *
 * The browser cannot record a choice yet (the standard console is
 * read-only), so the model carries, per choice, what happens after it and
 * the exact command an agent runs to record it. `formSpec` is kept only for
 * the live path, which renders when console readiness allows it.
 *
 * No node builtins imported here → safe from the server component; the returned
 * shape is fully serializable so it can cross into the client AdjudicationCard.
 */

const AREA = "memory-adjudicate";

export type MemoryChoiceView = {
  value: string;
  label: string;
  /** what happens after this choice, in one plain sentence */
  means: string;
  /** the exact command an agent runs to record it (real id, no blanks) */
  command: string;
};

export type AdjudicationCardModel = {
  id: string;
  kind: MemoryChoiceFor;
  /** "Proposed memory" | "Proposed rule" */
  kindLabel: string;
  /** "Waiting for you" */
  stateLabel: string;
  headline: string;
  fullStatement: string;
  /** what it is about, in words; `href` is the app's page when the subject is an app */
  about: { label: string; href: string | null; raw: string } | null;
  /** where it came from, in one line; `href` opens the evidence or record history */
  from: { text: string; href: string | null };
  createdAt: string;
  choices: MemoryChoiceView[];
  /** raw qualities, behind the agents' disclosure */
  technical: { label: string; value: string }[];
  evidence: string[];
  /** Entity slug (or claim id) to seed the embedded memory graph from this card. */
  seed: string;
  /** cards with the same subject and source group together */
  groupKey: string;
  /** live path only (console readiness): the registry's options and fields */
  formSpec: DecisionFormSpec;
};

function sourceText(candidate: MemoryCandidate): { text: string; href: string | null } {
  const source = memorySource(candidate);
  if (source) return { text: source.title, href: source.ref ? evidenceHistoryHref(source.ref) : evidenceHistoryHref(candidate.id) };
  return { text: "No source was recorded with this proposal", href: evidenceHistoryHref(candidate.id) };
}

export function buildAdjudicationModel(
  candidate: MemoryCandidate,
  area: string = AREA,
): AdjudicationCardModel | null {
  const spec = specFor(area);
  if (!spec) return null;

  const options = spec.optionsFor ? spec.optionsFor(candidate) : spec.options;
  if (!options.length) return null;

  const kind: MemoryChoiceFor = area === "standing-law-candidate" ? "rule" : "memory";
  const pl = spec.plainLanguage(candidate);
  const defaultOption = spec.defaultOption ?? options[0].value;
  const statement = memoryStatement(candidate.object);
  const subject = memorySubject(str(candidate.entity));
  const qualifiers = (candidate.qualifiers ?? {}) as Record<string, unknown>;
  const refs = [...new Set([
    ...pl.evidence ?? [],
    ...(Array.isArray(candidate.evidence_refs) ? candidate.evidence_refs.filter((r): r is string => typeof r === "string") : []),
    str(qualifiers.source_ref),
  ].filter((ref) => ref && ref !== str(qualifiers.source) && ref !== candidate.id))];

  // A proposed rule's one provenance line is the registry's plain "where
  // from" ("From a decision you confirmed, July 30"); a proposed memory's is
  // the heading of the source it was taken from.
  const from = kind === "rule"
    ? { text: pl.body || "No source was recorded with this proposal", href: evidenceHistoryHref(candidate.id) }
    : sourceText(candidate);

  const choices: MemoryChoiceView[] = options.map((option) => {
    const words = memoryChoice(option.value, kind);
    const argv = spec.buildArgv?.({ itemId: candidate.id, item: candidate, option: option.value, fields: {} }) ?? option.argv ?? null;
    return { value: option.value, label: words.label, means: words.means, command: argv?.length ? `compound ${argv.join(" ")}` : "" };
  });

  const technical: { label: string; value: string }[] = [];
  if (str(candidate.entity)) technical.push({ label: "Subject", value: str(candidate.entity) });
  if (str(candidate.predicate)) technical.push({ label: "Relation", value: str(candidate.predicate) });
  if (candidate.confidence != null && candidate.confidence !== "") technical.push({ label: "Confidence", value: String(candidate.confidence) });
  if (str(candidate.derivation)) technical.push({ label: "How it was found", value: str(candidate.derivation) });
  if (str(candidate.promotion_basis)) technical.push({ label: "Reason given", value: memoryBasis(str(candidate.promotion_basis)) });
  if (str(candidate.visibility)) technical.push({ label: "Who may be told", value: str(candidate.visibility) });
  if (str(candidate.state)) technical.push({ label: "Recorded state", value: str(candidate.state) });

  const terms = kind === "rule" ? KNOWLEDGE_TERMS.proposedRule : KNOWLEDGE_TERMS.proposedMemory;
  return {
    id: candidate.id,
    kind,
    kindLabel: sentence(terms.name),
    stateLabel: "Waiting for you",
    headline: statement.summary,
    fullStatement: statement.full,
    about: subject ? { label: subject.label, href: subject.app ? appHref(subject.app) : null, raw: subject.raw } : null,
    from,
    createdAt: str(candidate.created_at),
    choices,
    technical,
    evidence: refs,
    seed: str(candidate.entity) || candidate.id,
    groupKey: kind === "rule" ? candidate.id : `${str(candidate.entity)}\u0000${from.text}`,
    formSpec: { area: spec.area, options, fields: spec.fields, defaultOption },
  };
}

/**
 * Proposed memories with the same subject and source, as one group (ten
 * notes taken from one paper are one judgment, not ten scrolls). Proposed
 * rules are never grouped: each is its own decision and its own link target.
 * Order is kept: a group sits where its first card was.
 */
export function groupAdjudicationModels(models: readonly AdjudicationCardModel[]): AdjudicationCardModel[][] {
  const groups = new Map<string, AdjudicationCardModel[]>();
  for (const model of models) {
    const key = model.kind === "rule" ? `rule\u0000${model.id}` : model.groupKey;
    const list = groups.get(key);
    if (list) list.push(model);
    else groups.set(key, [model]);
  }
  return [...groups.values()];
}
