export function remainingDecideMembers<T extends { id: string }>(
  members: T[],
  excluded: ReadonlySet<string>,
  completed: ReadonlySet<string>,
): T[] {
  return members.filter((member) => !excluded.has(member.id) && !completed.has(member.id));
}

export function partitionDecideGroupsByOwnerWorkflow<
  T extends { capability: { kind: string } },
>(groups: T[]): { ready: T[]; limited: T[] } {
  const ready: T[] = [];
  const limited: T[] = [];
  for (const group of groups) {
    (group.capability.kind === "unavailable" ? limited : ready).push(group);
  }
  return { ready, limited };
}
