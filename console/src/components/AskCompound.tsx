"use client";

import type { FormEvent, ReactNode } from "react";
import { usePathname } from "next/navigation";
import { useState } from "react";

import { Button, Drawer, SparkIcon, StatusChip } from "@/components/kit";

const DEFAULT_SUGGESTIONS = ["What needs me?", "Why is work blocked?", "Compare the top decision", "Show the freshest evidence"];

function routeContext(pathname: string): { label: string; suggestions: string[] } {
  if (pathname === "/live" || pathname.startsWith("/traffic") || pathname.startsWith("/executors")) {
    return {
      label: "Live operations",
      suggestions: ["Which agents are working?", "Where is work waiting?", "Show active work by owner", "What needs intervention?"],
    };
  }
  if (pathname === "/work" || pathname.startsWith("/work/")) {
    return {
      label: "Work",
      suggestions: ["Why is work blocked?", "Show running work", "What can move next?", "Who owns the oldest active work?"],
    };
  }
  if (pathname === "/reviews") {
    return {
      label: "Decide",
      suggestions: ["Compare the top decision", "What happens if I wait?", "Show the freshest evidence", "Which decision has the widest impact?"],
    };
  }
  if (pathname === "/") {
    return {
      label: "Today",
      suggestions: ["What needs me now?", "What changed most recently?", "What can Compound continue safely?", "What would help most next?"],
    };
  }
  return { label: "Current view", suggestions: DEFAULT_SUGGESTIONS };
}

export function AskCompound() {
  const pathname = usePathname();
  const context = routeContext(pathname);
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<ReactNode[]>([]);
  const [error, setError] = useState("");

  async function compose(value: string) {
    const trimmed = value.trim();
    if (!trimmed) return;
    setQuery(trimmed);
    setBusy(true);
    setError("");
    try {
      const [{ browserAiConsole }, { renderViewInvocation }] = await Promise.all([
        import("@/ai-console"),
        import("@/lib/view-registry"),
      ]);
      const proposal = await browserAiConsole.proposeView(trimmed, {
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
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Ask Compound could not compose this view.");
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
        className="fixed bottom-5 right-5 z-50 inline-flex h-10 items-center gap-2 rounded-pill border border-hair bg-panel pl-2 pr-4 text-sm font-medium text-ink shadow-lg transition-colors hover:bg-soft"
        title={`Ask Compound about ${context.label.toLowerCase()}`}
      >
        <span className="flex h-6.5 w-6.5 items-center justify-center rounded-full bg-work text-panel" aria-hidden>
          <SparkIcon size={14} />
        </span>
        Ask Compound
      </button>

      <Drawer
        open={open}
        onOpenChange={setOpen}
        title="Ask Compound"
        meta={`${context.label} · explore the current records`}
        size="wide"
        contextual
        footer={<span className="text-2xs text-faint">Answers help you understand the work. They do not start or change tasks.</span>}
      >
        <div className="flex flex-col gap-6">
          <div className="composer-stage rounded-xl px-4 py-4">
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div>
                <div className="section-label text-route">Understand this moment</div>
                <p className="mt-2 max-w-xl text-md font-semibold leading-snug text-ink">
                  Get help understanding {context.label.toLowerCase()}.
                </p>
              </div>
              <StatusChip tone="work" label="View only" />
            </div>
            <p className="mt-2 max-w-xl text-xs leading-relaxed text-muted">
              Choose a suggested question or ask your own. Answers use the records available to this console.
            </p>
          </div>

          <form onSubmit={submit} className="flex flex-col gap-2">
            <label htmlFor="ask-compound-query" className="text-sm font-semibold text-ink">What do you need to understand?</label>
            <textarea
              id="ask-compound-query"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              rows={4}
              placeholder="Show what is blocked by me, why, and what moves if I decide."
              className="w-full resize-none rounded-xl border border-hair bg-soft px-4 py-3 text-sm leading-relaxed text-ink outline-none placeholder:text-faint focus:border-route-line"
            />
            <div className="flex justify-end"><Button variant="primary" type="submit" disabled={busy || !query.trim()}>{busy ? "Looking it up…" : "Show answer"}</Button></div>
          </form>

          <div className="flex flex-wrap gap-2">
            {context.suggestions.map((suggestion) => (
              <button
                key={suggestion}
                type="button"
                onClick={() => void compose(suggestion)}
                className="rounded-pill border border-hair bg-soft px-3 py-1.5 text-xs text-muted transition-colors hover:border-route-line hover:bg-route-soft hover:text-route"
              >
                {suggestion}
              </button>
            ))}
          </div>

          {error ? (
            <div role="alert" className="rounded-lg border border-crit-line bg-crit-soft px-3 py-2.5 text-xs leading-relaxed text-crit">
              {error} Try again, or open Work or Decide to review the records.
            </div>
          ) : null}

          {result.length ? (
            <section aria-live="polite" className="border-t border-hair pt-5">
              <div className="mb-3 flex items-center justify-between gap-3">
                <div>
                  <div className="section-label">Answer</div>
                  <p className="mt-1 text-xs text-muted">Based on the available records.</p>
                </div>
                <StatusChip tone="good" label="Recorded information" />
              </div>
              <div className="grid gap-4 md:grid-cols-2">{result}</div>
            </section>
          ) : null}
        </div>
      </Drawer>
    </>
  );
}
