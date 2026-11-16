"use client";

import { useRouter } from "next/navigation";
import { useEffect, useState, useSyncExternalStore, useTransition } from "react";

/**
 * Automatic refresh, remembered in this browser and honoured by every timer
 * that re-renders the page (this control's 30 s tick and PendingRefresh's
 * follow-up while reads are loading). "Refresh" asks the server for the page
 * now and stays busy until the reads it started have landed.
 */
export const AUTO_REFRESH_KEY = "compound-auto-refresh";
const AUTO_REFRESH_MS = 30_000;
/** How long a manual refresh keeps following reads that are still loading. */
export const MANUAL_FOLLOW_MS = 60_000;

type RefreshState = { auto: boolean; followUntil: number };
let state: RefreshState = { auto: true, followUntil: 0 };
let loaded = false;
const listeners = new Set<() => void>();

function load() {
  if (loaded || typeof window === "undefined") return;
  loaded = true;
  try {
    state = { ...state, auto: window.localStorage.getItem(AUTO_REFRESH_KEY) !== "off" };
  } catch {
    // storage blocked: keep the default (on)
  }
}

function emit(next: Partial<RefreshState>) {
  state = { ...state, ...next };
  listeners.forEach((listener) => listener());
}

export function setAutoRefresh(auto: boolean) {
  load();
  try {
    window.localStorage.setItem(AUTO_REFRESH_KEY, auto ? "on" : "off");
  } catch {
    // storage blocked: honour it for this view only
  }
  emit({ auto });
}

/** May a timer re-render the page now? Auto-refresh on, or a manual refresh still following its reads. */
export function refreshAllowed(nowMs: number): boolean {
  load();
  return state.auto || nowMs < state.followUntil;
}

/** Start following the reads a manual refresh started; `null` stops following. */
export function followManualRefresh(nowMs: number | null) {
  emit({ followUntil: nowMs === null ? 0 : nowMs + MANUAL_FOLLOW_MS });
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

function useRefreshState(): RefreshState {
  return useSyncExternalStore(
    subscribe,
    () => {
      load();
      return state;
    },
    () => state,
  );
}

/** Reads still loading on the page (PendingRefresh marks them). */
function readsPendingOnPage(): boolean {
  return typeof document !== "undefined" && Boolean(document.querySelector('[data-reads-pending="true"]'));
}

export function RefreshControl({ responsive = false }: { responsive?: boolean }) {
  const router = useRouter();
  const { auto, followUntil } = useRefreshState();
  const [tick, setTick] = useState(0);
  const [pending, startTransition] = useTransition();
  const [now, setNow] = useState(0);

  useEffect(() => {
    if (!auto) return;
    const id = setInterval(() => {
      startTransition(() => router.refresh());
      setTick((t) => t + 1);
    }, AUTO_REFRESH_MS);
    return () => clearInterval(id);
  }, [auto, router]);

  // While a manual refresh follows its reads, check each second whether they landed.
  useEffect(() => {
    if (!followUntil) return;
    const id = setInterval(() => {
      const at = Date.now();
      setNow(at);
      if (at >= followUntil || (!pending && !readsPendingOnPage())) followManualRefresh(null);
    }, 1_000);
    return () => clearInterval(id);
  }, [followUntil, pending]);

  const busy = pending || (followUntil > 0 && now < followUntil);
  function refreshNow() {
    followManualRefresh(Date.now());
    setNow(Date.now());
    startTransition(() => router.refresh());
  }

  return (
    <div className="flex shrink-0 items-center" data-tick={tick}>
      <div
        data-refresh-control="expanded"
        className={responsive ? "hidden shrink-0 items-center gap-2 sm:flex" : "flex shrink-0 items-center gap-2"}
      >
        <button
          type="button"
          aria-pressed={auto}
          onClick={() => setAutoRefresh(!auto)}
          className={`inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-lg border border-hair px-2.5 py-1 text-xs font-medium transition-colors hover:bg-soft ${
            auto ? "text-ink" : "text-faint"
          }`}
          title={auto ? "The page updates every 30 seconds. This browser remembers the choice." : "The page updates only when you press Refresh. This browser remembers the choice."}
        >
          <span className={`h-1.5 w-1.5 rounded-full ${auto ? "bg-good" : "bg-faint"}`} aria-hidden />
          Auto-refresh {auto ? "on" : "off"}
        </button>
        <button
          type="button"
          aria-label={busy ? "Refreshing" : "Refresh now"}
          onClick={refreshNow}
          disabled={busy}
          className="shrink-0 whitespace-nowrap rounded-lg border border-hair px-2.5 py-1 text-xs font-medium text-ink transition-colors hover:bg-soft disabled:cursor-wait disabled:opacity-60"
        >
          {busy ? "Refreshing…" : "Refresh"}
        </button>
      </div>
      {responsive ? (
        <div data-refresh-control="compact" className="flex shrink-0 items-center gap-1 sm:hidden">
          <button
            type="button"
            aria-pressed={auto}
            aria-label={`Automatic refresh every 30 seconds ${auto ? "on" : "off"}`}
            onClick={() => setAutoRefresh(!auto)}
            className={`inline-flex h-8 w-9 shrink-0 items-center justify-center whitespace-nowrap rounded-md text-2xs font-semibold tabular-nums ${
              auto ? "bg-good-soft text-good" : "bg-soft text-muted"
            }`}
            title={`Automatic refresh every 30 seconds ${auto ? "on" : "off"}`}
          >
            30s
          </button>
          <button
            type="button"
            aria-label={busy ? "Refreshing" : "Refresh now"}
            title={busy ? "Refreshing" : "Refresh now"}
            onClick={refreshNow}
            disabled={busy}
            className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md border border-hair bg-panel text-base text-muted hover:bg-soft disabled:cursor-wait disabled:opacity-60"
          >
            <span aria-hidden>{busy ? "…" : "↻"}</span>
          </button>
        </div>
      ) : null}
    </div>
  );
}
