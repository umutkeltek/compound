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
  return [{
    mode: "generic",
    area: group.area,
    itemId: group.sampleId,
    actionId: "review_resolution_park",
    expectedRevision: "",
    label: "Review disposition",
    argv: null,
    decisionOptions: [
      {
        value: "review_resolution_park",
        label: "Park",
        tone: "orange",
        description: "Files this review as parked with your reason. The underlying authority stays unchanged, and the disposition can be undone.",
      },
      {
        value: "review_resolution_dismiss",
        label: "Dismiss",
        tone: "gray",
        description: "Files this review as dismissed with your reason. The underlying authority stays unchanged, and the disposition can be undone.",
      },
    ],
    defaultOption: "review_resolution_park",
    requiresConfirmation: true,
    copyCommand,
    effectKind: "record",
    reasonRequired: true,
  }];
}
