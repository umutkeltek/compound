/*
 * Client-safe coordination model: the types + pure predicates the
 * CoordinationBoard renders. NO node: builtins, NO lib/compound import — this
 * module lands in the client bundle, so it must stay leaf-pure (the
 * client-bundle-guard smoke enforces the boundary). The server data plane
 * (lib/coordination.ts) maps `compound coordination --json` onto these types.
 */

export type CoordinationLane = {
  state: string;
  remaining: number;
  resetsAt: string;
  refused: boolean;
  confirmationRequired: boolean;
  message: string;
};

export type CoordinationDoing = {
  workId: string;
  title: string;
  state: string;
  nextAction: string;
  claimedBy: string;
  owner: string;
};

export type CoordinationActor = {
  actor: string;
  doing: CoordinationDoing | null;
  nextAction: string;
  canContinue: boolean;
  waitReason: string;
  blockedOn: string[];
  lane: CoordinationLane | null;
  open: number;
  inProgress: number;
  needsReview: number;
};

export type CoordinationWait = {
  work: string;
  actor: string;
  reason: string;
  blockedOn: string[];
  lane: CoordinationLane | null;
};

export type OperatorCoordinationDelta = {
  kind: string;
  subjectRef: string;
  before: string;
  after: string;
};

export type OperatorCoordinationItem = {
  eventRef: string;
  workRef: string;
  deliveryClass: string;
  delta: OperatorCoordinationDelta;
  artifactRefs: string[];
  semanticRevision: string;
};

export type OperatorCoordinationProjection = {
  schemaVersion: string;
  revision: string;
  total: number;
  truncated: boolean;
  items: OperatorCoordinationItem[];
};

const OPERATOR_COORDINATION_MAX_ITEMS = 8;

function projectionString(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function projectionNumber(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function projectionArray(value: unknown): unknown[] {
  return Array.isArray(value) ? value : [];
}

// normalizeOperatorCoordination is a pure client-side whitelist. Unknown and
// hostile fields are discarded instead of crossing into rendered props.
export function normalizeOperatorCoordination(
  value: unknown,
  humanOnly = false,
): OperatorCoordinationProjection {
  const raw = value && typeof value === "object" && !Array.isArray(value)
    ? value as Record<string, unknown>
    : {};
  const eligible = projectionArray(raw.items)
    .map((candidate): OperatorCoordinationItem => {
      const item = candidate && typeof candidate === "object" && !Array.isArray(candidate)
        ? candidate as Record<string, unknown>
        : {};
      const rawDelta = item.delta && typeof item.delta === "object" && !Array.isArray(item.delta)
        ? item.delta as Record<string, unknown>
        : {};
      return {
        eventRef: projectionString(item.event_ref),
        workRef: projectionString(item.work_ref),
        deliveryClass: projectionString(item.delivery_class),
        delta: {
          kind: projectionString(rawDelta.kind),
          subjectRef: projectionString(rawDelta.subject_ref),
          before: projectionString(rawDelta.before),
          after: projectionString(rawDelta.after),
        },
        artifactRefs: projectionArray(item.artifact_refs).map(projectionString).filter(Boolean),
        semanticRevision: projectionString(item.semantic_revision),
      };
    })
    .filter((item) => !humanOnly || item.deliveryClass === "human_attention");
  const items = eligible.slice(0, OPERATOR_COORDINATION_MAX_ITEMS);
  const rawTotal = Math.max(0, projectionNumber(raw.total));
  const schemaVersion = projectionString(raw.schema_version);
  const total = humanOnly && !schemaVersion.startsWith("compound.operator_attention_projection.")
    ? eligible.length
    : Math.max(rawTotal, eligible.length);
  return {
    schemaVersion,
    revision: projectionString(raw.revision),
    total,
    truncated: raw.truncated === true || eligible.length > items.length || total > items.length,
    items,
  };
}

export type CoordinationView = {
  /** false => the coordination verb never answered (old binary / door down). */
  present: boolean;
  actor: string;
  actorExplicit: boolean;
  /** every actor the view can be re-centered on (generic; me first). */
  actorOptions: string[];
  me: CoordinationActor;
  others: CoordinationActor[];
  waits: CoordinationWait[];
  operatorCoordination: OperatorCoordinationProjection;
  integrity: string[];
  generatedAt: string;
};

/** A lane is a gate (a wait) when the guard refused, holds for confirmation,
 *  or reports a closed state — mirrors the verb's coordinationLaneGated. */
export function laneGated(lane: CoordinationLane | null): boolean {
  if (!lane) return false;
  if (lane.refused || lane.confirmationRequired) return true;
  return lane.state === "walled" || lane.state === "dead" || lane.state === "refused";
}
