export type BriefingGateCandidate = {
  id: string;
  title: string;
  consequence: string;
  downstreamWorkCount: number;
  createdAt?: string | number | Date | null;
  source?: string;
};

export type BriefingInsight = BriefingGateCandidate & {
  ageMs: number | null;
};

function ageMs(value: BriefingGateCandidate["createdAt"]): number | null {
  if (value == null || value === "") return null;
  const date = value instanceof Date ? value : new Date(value);
  const ms = date.getTime();
  if (Number.isNaN(ms)) return null;
  return Date.now() - ms;
}

/**
 * Select the pending gate whose clearing unblocks the most downstream work.
 * Age is only a deterministic tie-breaker after leverage.
 */
export function selectHighestLeverageGate(
  candidates: BriefingGateCandidate[],
): BriefingInsight | null {
  const ranked = candidates
    .filter((candidate) => candidate.downstreamWorkCount > 0)
    .map((candidate) => ({ ...candidate, ageMs: ageMs(candidate.createdAt) }))
    .sort((a, b) => {
      if (b.downstreamWorkCount !== a.downstreamWorkCount) {
        return b.downstreamWorkCount - a.downstreamWorkCount;
      }
      if (a.ageMs != null && b.ageMs != null && b.ageMs !== a.ageMs) {
        return b.ageMs - a.ageMs;
      }
      if (a.ageMs != null && b.ageMs == null) return -1;
      if (a.ageMs == null && b.ageMs != null) return 1;
      return a.title.localeCompare(b.title);
    });

  return ranked[0] ?? null;
}

export function humanAge(value: unknown): string {
  const date = value instanceof Date ? value : value ? new Date(value as string | number) : null;
  if (!date || Number.isNaN(date.getTime())) return "";
  const seconds = Math.max(0, Math.floor((Date.now() - date.getTime()) / 1000));
  if (seconds < 90) return "now";
  if (seconds < 90 * 60) return `${Math.round(seconds / 60)}m`;
  if (seconds < 36 * 3600) return `${Math.round(seconds / 3600)}h`;
  return `${Math.round(seconds / 86400)}d`;
}

export function isOld(value: unknown): boolean {
  const date = value instanceof Date ? value : value ? new Date(value as string | number) : null;
  if (!date || Number.isNaN(date.getTime())) return false;
  return Date.now() - date.getTime() >= 36 * 3600 * 1000;
}
