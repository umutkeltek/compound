import { ProductLoopCard } from "@/components/products/ProductLoopCard";
import { EmptyState, Stat } from "@/components/ui";
import { getProductLoops, getProductPortfolio } from "@/lib/products";

export const dynamic = "force-dynamic";

export default async function LoopsPage() {
  const [loops, portfolio] = await Promise.all([getProductLoops(), getProductPortfolio()]);

  const withSignals = loops.filter((l) => l.signalCount > 0).length;
  const reviewReady = portfolio.reduce((n, p) => n + p.reviewReadyCount, 0);
  const totalSignals = loops.reduce((n, l) => n + l.signalCount, 0);

  return (
    <div className="flex flex-col gap-6">
      <div>
        <h1 className="text-[22px] font-semibold tracking-tight">Feedback loops</h1>
        <p className="mt-0.5 text-[12.5px] text-ink-3">
          One shape for anything with outputs and feedback — change → signal → hypothesis → review → next move.
          Today these are your apps; the same loop holds any product, channel, or workflow as sources connect.
        </p>
      </div>

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Stat label="Loops" value={loops.length} glyph="↻" accent="blue" />
        <Stat label="With live signals" value={withSignals} glyph="↯" accent="green" sub={`${totalSignals} signals total`} />
        <Stat label="Review-ready" value={reviewReady} glyph="✓" accent="orange" sub="matched signal + hypothesis" />
        <Stat
          label="Awaiting data"
          value={loops.length - withSignals}
          glyph="○"
          accent="gray"
          sub="connected, no signal yet"
        />
      </div>

      {loops.length === 0 ? (
        <EmptyState>
          No loops yet. A loop starts when a subject gets a signal — e.g. register an app with{" "}
          <code className="mono">compound product import &lt;product.yaml&gt;</code> and feed it a metric.
        </EmptyState>
      ) : (
        <div className="grid gap-4 lg:grid-cols-2">
          {loops.map((loop) => (
            <ProductLoopCard key={loop.productId} loop={loop} />
          ))}
        </div>
      )}

      <p className="mono text-[10.5px] leading-snug text-ink-3">
        source: compound product loops --json · compound product authority --json · Connected sources: App Store Connect,
        Search Console, canonical metrics. Shape-supported (no puller yet): RevenueCat, PostHog. Not connected:
        TelemetryDeck.
      </p>
    </div>
  );
}
