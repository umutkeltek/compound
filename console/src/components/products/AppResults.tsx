import { ProductLoopCard } from "@/components/products/ProductLoopCard";
import type { ProductVerdict } from "@/lib/attention";
import type { ProductLoop } from "@/lib/products";

/**
 * "Did what we changed help?" for one app: its one results card (every
 * spelling of the app folded in), or one plain sentence when there is none
 * or the read has not landed.
 */
export function AppResults({ name, loop, known, verdict }: { name: string; loop: ProductLoop | null; known: boolean; verdict?: ProductVerdict }) {
  return (
    <section id="app-results" className="flex scroll-mt-24 flex-col gap-3" aria-labelledby="app-results-title">
      <h2 id="app-results-title" className="text-lg font-semibold tracking-tight text-ink">Did the changes help?</h2>
      {loop ? (
        <ProductLoopCard loop={loop} verdict={verdict} />
      ) : known ? (
        <p className="text-sm text-muted">No change to {name} has been recorded with a measurement yet, so there is nothing to judge.</p>
      ) : (
        <p className="text-sm text-muted">Still reading results; this can take a minute.</p>
      )}
    </section>
  );
}
