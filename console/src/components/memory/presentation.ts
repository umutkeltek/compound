/** Display recorded words without guessing what an unfamiliar predicate means. */
export function memoryStatement(object: unknown): { summary: string; full: string } {
  const full = typeof object === "string" ? object.trim() : "";
  if (!full) return { summary: "The statement is missing. Open the record history before deciding.", full: "" };
  return { summary: full.length > 420 ? `${full.slice(0, 420).trimEnd()}…` : full, full };
}

export function memoryState(state: string): string {
  return ({ proposed: "Awaiting review", parked: "Set aside", promoted: "Accepted for recall", rejected: "Rejected", no_promote: "Kept out of recall" } as Record<string, string>)[state] ?? (state || "State not recorded");
}

export function evidenceDestination(ref: string): { href: string; label: string } | null {
  // Only known record identities have an owner view. A path or arbitrary URI
  // must never become an invented destination or an executable browser URL.
  const work = /^(?:review-fallback-work:)?(work_[A-Za-z0-9_-]+)$/.exec(ref);
  if (work) return { href: `/work/${encodeURIComponent(work[1])}`, label: "Open work" };
  if (/^(?:ev_|cand_|claim_)[A-Za-z0-9_-]+$/.test(ref) || /^DEC-[A-Za-z0-9_-]+$/.test(ref)) {
    return { href: `/map/lineage/${encodeURIComponent(ref)}`, label: ref.startsWith("ev_") ? "Read evidence history" : "Read record history" };
  }
  return null;
}
