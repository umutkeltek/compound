/*
 * Client-side leverage ranking lived here (`selectHighestLeverageGate` + its
 * BriefingGateCandidate/BriefingInsight types + `ageMs`). Phase 2 deleted it:
 * ranking is now the server-side job of `compound attention --json`, whose
 * `top` group IS the highest-leverage gate. The briefing consumes that
 * directly. Only the age-formatting helpers below remain (pure presentation).
 */

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
