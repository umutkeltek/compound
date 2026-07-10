export function remainingDecideMembers<T extends { id: string }>(
  members: T[],
  excluded: ReadonlySet<string>,
  completed: ReadonlySet<string>,
): T[] {
  return members.filter((member) => !excluded.has(member.id) && !completed.has(member.id));
}
