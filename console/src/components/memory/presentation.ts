import { humanizeProductId } from "@/lib/app-identity";

/** Display recorded words without guessing what an unfamiliar predicate means. */
export function memoryStatement(object: unknown): { summary: string; full: string } {
  const full = typeof object === "string" ? object.trim() : "";
  if (!full) return { summary: "The statement is missing. Open the record history before deciding.", full: "" };
  return { summary: full.length > 420 ? `${full.slice(0, 420).trimEnd()}…` : full, full };
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

/**
 * What a memory is about, in words, from its subject (`entity`). An app's id
 * reads as the app's name and links to its page; a decision record has no
 * subject worth naming. The raw subject stays available as a tooltip.
 */
export function memorySubject(entity: string): { label: string; app: string | null; raw: string } | null {
  const raw = (entity ?? "").trim();
  if (!raw || raw.startsWith("decision:")) return null;
  const words = (value: string) => value.replace(/[_-]+/g, " ").replace(/\s+/g, " ").trim();
  if (raw.startsWith("product:")) {
    return { label: humanizeProductId(raw) || raw, app: raw, raw };
  }
  if (raw.startsWith("portfolio:")) return { label: `All apps: ${words(raw.slice("portfolio:".length))}`, app: null, raw };
  if (raw.startsWith("knowledge:")) return { label: `Reading material: ${words(raw.slice("knowledge:".length).replace(/:/g, " "))}`, app: null, raw };
  return { label: words(raw.replace(/:/g, " ")), app: null, raw };
}

/** First readable line of a quoted source (a page's heading), without markdown marks, shortened. */
export function sourceHeading(quote: unknown, max = 110): string {
  const text = typeof quote === "string" ? quote : "";
  const line = text.split("\n").map((part) => part.replace(/^[#>*\s-]+/, "").trim()).find(Boolean) ?? "";
  if (line.length <= max) return line;
  const cut = line.slice(0, max);
  const space = cut.lastIndexOf(" ");
  return `${(space > max * 0.6 ? cut.slice(0, space) : cut).trimEnd()}…`;
}

/** Where the memory came from, from the evidence the list read returned: the source's own heading, else its page. */
export function memorySource(candidate: Record<string, unknown>): { title: string; ref: string } | null {
  const evidence = candidate.evidence && typeof candidate.evidence === "object" && !Array.isArray(candidate.evidence)
    ? (candidate.evidence as Record<string, unknown>)
    : {};
  const isEv = (value: unknown): value is string => typeof value === "string" && /^ev_[A-Za-z0-9_-]+$/.test(value);
  const listed = Array.isArray(candidate.evidence_refs) ? candidate.evidence_refs.find(isEv) : undefined;
  const ref = isEv(evidence.ref) ? evidence.ref : isEv(candidate.source_trace_ref) ? candidate.source_trace_ref : listed ?? "";
  const heading = sourceHeading(evidence.quote);
  if (heading) return { title: heading, ref };
  const qualifiers = candidate.qualifiers && typeof candidate.qualifiers === "object" ? (candidate.qualifiers as Record<string, unknown>) : {};
  const page = typeof qualifiers.page_id === "string" ? qualifiers.page_id : "";
  if (page) return { title: `the recorded page “${page.replace(/[_-]+/g, " ")}”`, ref };
  return ref ? { title: "a recorded source", ref } : null;
}

/** The reason a proposal gives for trusting it (`promotion_basis`), in words. */
export function memoryBasis(basis: string): string {
  return ({
    curated_page: "a curated page",
    decision_record: "a decision record",
    explicit_confirmation: "you confirmed it",
    runtime_proof: "a recorded run",
    trusted_deterministic_import: "a trusted import",
  } as Record<string, string>)[basis] ?? basis.replace(/[_-]+/g, " ");
}
