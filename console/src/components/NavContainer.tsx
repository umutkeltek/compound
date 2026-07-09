import { Nav } from "@/components/Nav";
import { getReviews, getWorkbench, num } from "@/lib/compound";
import { needsYouGroupCount } from "@/lib/review-data";

/**
 * Server wrapper for Nav: fetches the badge counts through the existing
 * runJson TTL memo (so this doesn't slow every page — same cached
 * `compound reviews --json` / `compound workbench --json` calls other
 * pages already make) and hands plain numbers to the client Nav.
 *
 * Decide badge = needs_you group count (interim source: reviews groups,
 * until Phase 2 cuts every consumer over to `compound attention --json`).
 * Work badge = counts.needs_claim from the workbench projection — already
 * computed server-side there, so this is a free read off an existing fetch.
 */
export async function NavContainer() {
  const [reviews, workbench] = await Promise.all([getReviews(), getWorkbench()]);
  const decideCount = needsYouGroupCount(reviews);
  const workCount = num(workbench?.counts?.needs_claim);
  return <Nav decideCount={decideCount} workCount={workCount} />;
}
