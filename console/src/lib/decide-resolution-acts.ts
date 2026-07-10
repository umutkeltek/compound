import type { DecideAct, DecideGroup } from "@/lib/decide-model";

type GenericDecideAct = Extract<DecideAct, { mode: "generic" }>;

// Generic resolution controls are derived at render time so the serialized
// DecideGroup remains byte-identical for every existing card. Only honest-copy
// cards qualify; an already-actionable bespoke/generic/batch card gets none.
// The synthetic acts execute through the fixed server-side review-resolution
// adapter (argv is rebuilt server-side), so they carry no projection revision.
//
// This lives apart from decide-model.ts on purpose: it is imported by client
// components, and decide-model's module graph reaches node:child_process via
// @/lib/compound — type-only imports keep this file client-bundle-safe.
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
