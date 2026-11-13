"use client";

import { useRouter } from "next/navigation";
import { useEffect } from "react";

/**
 * While anything on the page is loading or updating, refresh the page every
 * few seconds; stop when nothing is pending. Cheap because a page render never
 * waits for compound: it only re-reads the cache, and the read queue's "due"
 * rule (not this timer) decides when compound actually runs.
 *
 * Several instances (page + sidebar) share one timer, so the page refreshes
 * once per interval, not once per instance. `stamp` changes on every server
 * render, so each render re-arms the timer while reads stay pending.
 */
let scheduled: ReturnType<typeof setTimeout> | null = null;

export const PENDING_REFRESH_MS = 5_000;

export function PendingRefresh({ pending, stamp }: { pending: boolean; stamp: number }) {
  const router = useRouter();
  useEffect(() => {
    if (!pending || scheduled) return;
    scheduled = setTimeout(() => {
      scheduled = null;
      if (typeof document !== "undefined" && document.visibilityState === "hidden") return;
      router.refresh();
    }, PENDING_REFRESH_MS);
  }, [pending, stamp, router]);
  return <span hidden data-reads-pending={pending ? "true" : "false"} />;
}
