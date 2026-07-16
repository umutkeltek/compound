import type { DecideGroup } from "./decide-model";

export function remainingDecideMembers<T extends { id: string }>(
  members: T[],
  excluded: ReadonlySet<string>,
  completed: ReadonlySet<string>,
): T[] {
  return members.filter((member) => !excluded.has(member.id) && !completed.has(member.id));
}

export type DecideReadinessSectionKey = "act_now" | "needs_context";

export type DecideReadinessSection<T> = {
  key: DecideReadinessSectionKey;
  label: string;
  description: string;
  groups: T[];
};

/**
 * The queue never guesses actionability from area names or UI copy. The
 * server-owned ObjectFace state is the only input to this client-safe split.
 */
export function decideReadinessSections<T extends { face: Pick<DecideGroup["face"], "state"> }>(
  groups: T[],
): DecideReadinessSection<T>[] {
  const actNow = groups.filter((group) => group.face.state === "ready");
  const needsContext = groups.filter((group) => group.face.state !== "ready");
  return [
    {
      key: "act_now" as const,
      label: "Act now",
      description: "A governed next step is ready.",
      groups: actNow,
    },
    {
      key: "needs_context" as const,
      label: "Needs context",
      description: "The owner workflow is missing or this group needs a narrower review.",
      groups: needsContext,
    },
  ].filter((section) => section.groups.length > 0);
}
