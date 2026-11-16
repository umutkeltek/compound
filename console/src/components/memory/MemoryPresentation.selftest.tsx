import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { buildAdjudicationModel } from "./model";
import { memoryStatement, evidenceDestination } from "./presentation";
import { EvidenceReference } from "./EvidenceReference";
import { DecisionContext } from "@/components/decide/DecisionContext";
import type { DecideGroup } from "@/lib/decide-model";

export function runMemoryPresentationSelfTest(): number {
  let checks = 0;
  const check = (value: unknown, message: string) => { assert.ok(value, message); checks++; };
  const object = "Keep deployments paused until the compatibility check passes.";
  const model = buildAdjudicationModel({
    id: "cand_0123456789ab", entity: "project:synthetic", predicate: "unknown_relation", object,
    state: "proposed", evidence_refs: ["ev_0123456789ab"], qualifiers: { source: "knowledge_page" },
  });
  check(model?.headline === object && model.body === "", "Native object text leads without invented predicate translation");
  check(model?.subject === "project:synthetic" && model.relation === "unknown_relation", "Exact raw subject and relation remain available");
  check(model?.stateLabel === "Awaiting review" && model.consequences?.hold.includes("not added to agent recall"), "Candidate state and set-aside consequence remain explicit");
  check(model?.formSpec.defaultOption === "park" && model.formSpec.options.some((o) => o.value === "promote"), "Presentation preserves registry action choices and conservative default");
  check(model?.evidence.includes("ev_0123456789ab") && !model.evidence.includes("knowledge_page"), "Provenance includes recorded refs, not source-kind labels");
  const long = "Recorded statement. ".repeat(40);
  check(memoryStatement(long).full === long.trim() && memoryStatement(long).summary.length < long.length, "Long statement retains exact full text for disclosure");
  check(memoryStatement("").summary.includes("statement is missing"), "Missing claim text reports a gap rather than using its entity as the claim");
  check(evidenceDestination("work_factory_20261116")?.href === "/work/work_factory_20261116", "Work links preserve underscored work ids");
  check(evidenceDestination("review-fallback-work:work_review_decisions")?.href === "/work/work_review_decisions", "Owner fallback-work refs reach the real work item");
  check(evidenceDestination("javascript:alert(1)") === null && evidenceDestination("ledger:/private/record.md") === null, "Unknown pointers and unsafe URI schemes get no invented browser destination");
  const evidence = renderToStaticMarkup(createElement(EvidenceReference, { reference: "ev_0123456789ab" }));
  check(evidence.includes('href="/map/lineage/ev_0123456789ab"') && evidence.includes("Read evidence history"), "Recorded evidence opens its real history owner");

  const base = {
    face: { owner: "Decision review" },
    members: [{ id: "work_factory_20261116", label: "Resolve review blocker", href: "/work/work_factory_20261116" }],
    evidence: [{ ref: "work_factory_20261116" }],
    brief: null,
  } as unknown as DecideGroup;
  const noRecommendation = renderToStaticMarkup(createElement(DecisionContext, { group: base }));
  check(noRecommendation.includes("Current owner") && noRecommendation.includes("Decision review"), "Current owner is visible without opening technical details");
  check(noRecommendation.includes("No recommendation is recorded") && !noRecommendation.includes("Prepared recommendation:"), "Missing recommendation stays unknown rather than adopting the default action");
  check(noRecommendation.includes("Resolve review blocker") && (noRecommendation.match(/href="\/work\/work_factory_20261116"/g) ?? []).length === 1, "Related work is meaningful, navigable, and deduplicated");
  const recommended = { ...base, brief: {
    options: [{ value: "park", label: "Keep aside" }],
    recommendation: { option: "park", rationale: "The source is incomplete." }, preparedBy: "fixture-reviewer",
  } } as unknown as DecideGroup;
  const recommendation = renderToStaticMarkup(createElement(DecisionContext, { group: recommended }));
  check(recommendation.includes("Prepared recommendation: Keep aside") && recommendation.includes("The source is incomplete.") && recommendation.includes("fixture-reviewer") && recommendation.includes("awaiting your choice"), "Recommendation carries rationale and author without implying operator approval");
  return checks;
}
