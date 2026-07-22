"use client";

import type { FormEvent, ReactNode } from "react";
import { useState } from "react";

import { Button, Drawer, StatusChip } from "@/components/kit";

const SUGGESTIONS = ["What needs me?", "Why is work blocked?", "Show the freshest evidence"];

export function AskCompound() {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<ReactNode[]>([]);

  async function compose(value: string) {
    const trimmed = value.trim();
    if (!trimmed) return;
    setQuery(trimmed);
    setBusy(true);
    try {
      const [{ stubAiConsole }, { renderViewInvocation }] = await Promise.all([
        import("@/ai-console"),
        import("@/lib/view-registry"),
      ]);
      const proposal = await stubAiConsole.proposeView(trimmed, {
        route: window.location.pathname,
        freshness: "current route projections",
      });
      setResult(proposal.components.map((invocation, index) => {
        const rendered = renderViewInvocation(invocation);
        return (
          <div key={`${invocation.view}-${index}`} className="min-w-0">
            {rendered.ok ? rendered.element : <p className="text-xs text-crit">{rendered.error}</p>}
          </div>
        );
      }));
    } finally {
      setBusy(false);
    }
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    void compose(query);
  }

  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="inline-flex h-7 items-center gap-1.5 rounded-pill border border-hair bg-panel px-2.5 text-xs font-semibold text-muted shadow-sm transition-colors hover:bg-soft hover:text-ink"
      >
        Ask <span className="hidden sm:inline">Compound</span>
        <span className="rounded-pill bg-route-soft px-1.5 py-0.5 text-2xs text-route">preview</span>
      </button>

      <Drawer
        open={open}
        onOpenChange={setOpen}
        title="Ask Compound"
        meta="view composition preview · governed actions stay outside the model"
        footer={<span className="text-2xs text-faint">The model composes the view; the registry owns the act.</span>}
      >
        <div className="flex flex-col gap-5">
          <div className="rounded-lg border border-route-line bg-route-soft px-3 py-2.5 text-xs leading-relaxed text-route">
            <div className="mb-1"><StatusChip tone="work" label="View only" /></div>
            This uses Compound&apos;s deterministic AI sandbox today. A Tambo adapter can replace it later without gaining
            mutation authority.
          </div>

          <form onSubmit={submit} className="flex flex-col gap-2">
            <label htmlFor="ask-compound-query" className="text-xs font-semibold text-muted">What do you want to understand?</label>
            <textarea
              id="ask-compound-query"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              rows={3}
              placeholder="Show what is blocked by me, why, and what moves if I decide."
              className="w-full resize-none rounded-lg border border-hair bg-soft px-3 py-2.5 text-sm text-ink outline-none placeholder:text-faint focus:border-work-line"
            />
            <div className="flex justify-end"><Button type="submit" disabled={busy || !query.trim()}>{busy ? "Composing…" : "Compose view"}</Button></div>
          </form>

          <div className="flex flex-wrap gap-2">
            {SUGGESTIONS.map((suggestion) => (
              <button
                key={suggestion}
                type="button"
                onClick={() => void compose(suggestion)}
                className="rounded-pill border border-hair bg-soft px-2.5 py-1 text-xs text-muted hover:bg-soft-2 hover:text-ink"
              >
                {suggestion}
              </button>
            ))}
          </div>

          {result.length ? (
            <section className="border-t border-hair pt-4">
              <div className="section-label mb-3">Composed from registered views</div>
              <div className="grid gap-3 sm:grid-cols-2">{result}</div>
            </section>
          ) : null}
        </div>
      </Drawer>
    </>
  );
}
