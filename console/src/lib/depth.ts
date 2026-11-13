import { cookies } from "next/headers";

/**
 * Server view of the Today/Live depth preference (DESIGN.md §10.1). The
 * browser keeps the preference in localStorage and mirrors it into the
 * `compound-experience-depth` cookie (ExperienceBar), so the server can skip
 * the reads of sections the current depth hides. Showing a section never
 * changes what it reads; only hidden sections skip their reads.
 */
export type Depth = "glance" | "understand" | "investigate" | "technical";

export const DEPTH_COOKIE = "compound-experience-depth";
export const DEFAULT_DEPTH: Depth = "understand";
const ORDER: Depth[] = ["glance", "understand", "investigate", "technical"];

export function parseDepth(value: unknown): Depth {
  return ORDER.includes(value as Depth) ? (value as Depth) : DEFAULT_DEPTH;
}

/** The request's depth; the default outside a request (tests, scripts). */
export async function requestDepth(): Promise<Depth> {
  try {
    return parseDepth((await cookies()).get(DEPTH_COOKIE)?.value);
  } catch {
    return DEFAULT_DEPTH;
  }
}

/** Mirrors globals.css `[data-depth-min]`: shown when depth >= min. */
export function depthShows(depth: Depth, min: Depth): boolean {
  return ORDER.indexOf(depth) >= ORDER.indexOf(min);
}
