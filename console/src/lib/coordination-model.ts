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
