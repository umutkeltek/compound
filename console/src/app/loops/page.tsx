import { PendingRefresh } from "@/components/PendingRefresh";
import { ProductLoopCard, loopState } from "@/components/products/ProductLoopCard";
import { EmptyState, FreshnessChip, LoopIcon, PageHeader, SectionHeader } from "@/components/kit";
import { getProductLoopsReport } from "@/lib/products";

export const dynamic = "force-dynamic";

/**
 * Loops — "how does output change the next move; which loop is blocked on me?"
 * (redesign plan Part 3). App results are the first shape; the same
 * change → signal → hypothesis → review → next-move loop holds any product,
 * channel, or workflow. Loops blocked on the operator (a verdict is due) float
 * to the top with the amber admission seam.
 */

function StatTile({
  label,
  value,
  sub,
  seam,
}: {
  label: string;
  value: number | string;
  sub?: string;
  seam?: boolean;
}) {
  const emphasise = seam && typeof value === "number" && value > 0;
  return (
    <div className="rounded-lg border border-hair bg-panel px-3 py-2.5 shadow-sm">
      <div className={`text-2xs font-bold uppercase tracking-wide ${emphasise ? "text-seam" : "text-faint"}`}>
        {label}
      </div>
      <div className={`mono mt-1 text-xl tabular-nums ${emphasise ? "text-seam" : "text-ink"}`}>{value}</div>
      {sub ? <div className="mt-0.5 text-2xs text-faint">{sub}</div> : null}
    </div>
  );
}

export default async function LoopsPage() {
  const { generatedAt, loops, status, checking, error } = await getProductLoopsReport();
  const known = status === "live" || status === "stale";

  const withSignals = loops.filter((l) => l.signalCount > 0).length;
  const totalSignals = loops.reduce((n, l) => n + l.signalCount, 0);
  const blockedOnYou = loops.filter((l) => loopState(l).blockedOnYou).length;

  // Loops blocked on the operator float to the top; the rest keep the
  // signal-volume order the projection returns.
  const ordered = loops
    .map((loop, index) => ({ loop, index, blocked: loopState(loop).blockedOnYou }))
    .sort((a, b) => (a.blocked === b.blocked ? a.index - b.index : a.blocked ? -1 : 1))
    .map((entry) => entry.loop);

  return (
    <div className="flex flex-col gap-6">
      <PendingRefresh pending={checking} stamp={Date.now()} />
      <PageHeader
        name="Results"
        action={known && !loops.length ? { label: "Review data connections", href: "/sources" } : undefined}
        lead="Did the last change help? Follow each product from change to evidence to next step."
        right={<FreshnessChip generatedAt={generatedAt} source="compound product loops --json" />}
      />
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <StatTile label="Apps tracked" value={known ? loops.length : "—"} sub="tracked" />
        <StatTile label="With observations" value={known ? withSignals : "—"} sub={known ? `${totalSignals} signals recorded` : "Checking signals"} />
        <StatTile label="Needs review" value={known ? blockedOnYou : "—"} sub="a verdict is due" seam />
        <StatTile label="Awaiting data" value={known ? loops.length - withSignals : "—"} sub="connected, no signal yet" />
      </div>

      <div id="app-results" tabIndex={-1} className="scroll-mt-6" />
      {loops.length === 0 && checking ? (
        <EmptyState>
          Loading product feedback. Results will appear here automatically.
        </EmptyState>
      ) : loops.length === 0 && status === "unreachable" ? (
        <div className="rounded-2xl border border-seam-line bg-seam-soft p-5 text-sm">
          <div className="font-semibold text-seam">Feedback loops could not be loaded.</div>
          <p className="mt-1 text-muted">
            <code className="mono">compound product loops --json</code> did not answer. This is a failed read, not an empty portfolio.
            {error ? <span className="mt-1 block text-xs text-faint">{error.slice(0, 240)}</span> : null}
          </p>
        </div>
      ) : loops.length === 0 ? (
        <EmptyState>
          No results yet. Connect an app and collect its first measurement to start tracking changes.
        </EmptyState>
      ) : (
        <section className="flex flex-col gap-3">
          <SectionHeader
            tone="product"
            icon={<LoopIcon />}
            label="App results"
            sub="Apps needing review appear first."
            right={<span className="mono text-2xs tabular-nums text-faint">{loops.length}</span>}
          />
          <div className="grid gap-4 lg:grid-cols-2">
            {ordered.map((loop) => (
              <ProductLoopCard key={loop.productId} loop={loop} />
            ))}
          </div>
        </section>
      )}

      {status === "stale" ? <p className="text-xs text-seam">Showing the last successful read{checking ? "; checking for updates" : ""}. {error ? "The latest refresh failed." : ""}</p> : null}
      <details className="text-xs text-muted"><summary className="cursor-pointer">Where these numbers come from</summary><p className="mt-2">Each loop is one registered product. Signal counts are recorded observations, not proof of an improvement.</p><code className="mono mt-2 block">compound product loops --json</code></details>
    </div>
  );
}
