"use client";

import { useRouter } from "next/navigation";
import { useEffect, useState, useTransition } from "react";

export function RefreshControl({ responsive = false }: { responsive?: boolean }) {
  const router = useRouter();
  const [auto, setAuto] = useState(true);
  const [tick, setTick] = useState(0);
  const [pending, startTransition] = useTransition();

  useEffect(() => {
    if (!auto) return;
    const id = setInterval(() => {
      startTransition(() => router.refresh());
      setTick((t) => t + 1);
    }, 30_000);
    return () => clearInterval(id);
  }, [auto, router]);

  return (
    <div className="flex shrink-0 items-center" data-tick={tick}>
      <div
        data-refresh-control="expanded"
        className={responsive ? "hidden shrink-0 items-center gap-2 sm:flex" : "flex shrink-0 items-center gap-2"}
      >
        <button
          type="button"
          aria-pressed={auto}
          onClick={() => setAuto(!auto)}
          className={`inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-lg border border-hair px-2.5 py-1 text-xs font-medium transition-colors hover:bg-soft ${
            auto ? "text-ink" : "text-faint"
          }`}
          title="Refresh every 30 seconds"
        >
          <span className={`h-1.5 w-1.5 rounded-full ${auto ? "bg-good" : "bg-faint"}`} aria-hidden />
          Auto-refresh {auto ? "on" : "off"}
        </button>
        <button
          type="button"
          aria-label={pending ? "Refreshing" : "Refresh now"}
          onClick={() => startTransition(() => router.refresh())}
          disabled={pending}
          className="shrink-0 whitespace-nowrap rounded-lg border border-hair px-2.5 py-1 text-xs font-medium text-ink transition-colors hover:bg-soft disabled:cursor-wait disabled:opacity-60"
        >
          {pending ? "Refreshing…" : "Refresh"}
        </button>
      </div>
      {responsive ? (
        <div data-refresh-control="compact" className="flex shrink-0 items-center gap-1 sm:hidden">
          <button
            type="button"
            aria-pressed={auto}
            aria-label={`Automatic refresh every 30 seconds ${auto ? "on" : "off"}`}
            onClick={() => setAuto(!auto)}
            className={`inline-flex h-8 w-9 shrink-0 items-center justify-center whitespace-nowrap rounded-md text-2xs font-semibold tabular-nums ${
              auto ? "bg-good-soft text-good" : "bg-soft text-muted"
            }`}
            title={`Automatic refresh every 30 seconds ${auto ? "on" : "off"}`}
          >
            30s
          </button>
          <button
            type="button"
            aria-label={pending ? "Refreshing" : "Refresh now"}
            title={pending ? "Refreshing" : "Refresh now"}
            onClick={() => startTransition(() => router.refresh())}
            disabled={pending}
            className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md border border-hair bg-panel text-base text-muted hover:bg-soft disabled:cursor-wait disabled:opacity-60"
          >
            <span aria-hidden>{pending ? "…" : "↻"}</span>
          </button>
        </div>
      ) : null}
    </div>
  );
}
