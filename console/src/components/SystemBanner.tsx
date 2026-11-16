"use client";

import { useEffect, useState } from "react";

/** One dismissal per source version: a newer gap shows the banner again. */
export const BANNER_DISMISS_KEY = "compound-build-banner-dismissed";

export type BannerFreshness = { status: "fresh" | "stale" | "missing" | "unknown"; installedBuild: string; head: string };

/** Show only a real, undismissed gap. `dismissedHead` is the source version he last dismissed. */
export function bannerVisible(freshness: BannerFreshness, dismissedHead: string | null): boolean {
  return freshness.status === "stale" && Boolean(freshness.head) && dismissedHead !== freshness.head;
}

export const BANNER_TEXT =
  "The installed Compound tool was built from older code than this console. If the newer code changed the tool, the agent that lands it installs the new build; nothing for you to do.";

/**
 * The build note in the top bar. It says in plain words what an older
 * installed tool means and who handles it (he never runs install commands),
 * and he can dismiss it until the source moves again. The layout passes the
 * freshness the server already holds (lib/compound.ts), so this adds no read.
 */
export function SystemBanner({ freshness, compact = false }: { freshness: BannerFreshness; compact?: boolean }) {
  // Hidden until the browser's dismissal is read, so a dismissed note never flashes.
  const [dismissed, setDismissed] = useState<string | null | undefined>(undefined);
  useEffect(() => {
    try {
      setDismissed(window.localStorage.getItem(BANNER_DISMISS_KEY));
    } catch {
      setDismissed(null);
    }
  }, []);
  if (dismissed === undefined || !bannerVisible(freshness, dismissed)) return null;
  function dismiss() {
    try {
      window.localStorage.setItem(BANNER_DISMISS_KEY, freshness.head);
    } catch {
      // storage blocked: hide for this view only
    }
    setDismissed(freshness.head);
  }
  return (
    <div
      role="status"
      data-build-banner
      className={compact ? "mx-4 mt-2 flex items-start gap-3 text-xs text-muted" : "mr-auto flex min-w-0 items-center gap-3 text-xs text-muted"}
    >
      <span className="min-w-0" title={`Installed build ${freshness.installedBuild || "unknown"}; source ${freshness.head}.`}>
        {BANNER_TEXT}
      </span>
      <button type="button" onClick={dismiss} className="shrink-0 rounded-md border border-hair px-2 py-0.5 font-medium text-ink hover:bg-soft">
        Dismiss
      </button>
    </div>
  );
}
