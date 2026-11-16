"use client";

import Link from "next/link";
import { useEffect, useState } from "react";

/**
 * "Back" on a work item's page returns to the Work list he came from: the
 * Work page remembers its last address (view, filter, agent, app, search)
 * for this browser tab, and the item page links back to it. A pasted link
 * or a fresh tab falls back to the Work page. Storage can be unavailable;
 * the link still works.
 */
const KEY = "compound.work.last-list";

/** Remember the Work list's address, without an open preview. */
export function rememberWorkList(search: string): void {
  const params = new URLSearchParams(search);
  params.delete("trace");
  const query = params.toString();
  try {
    window.sessionStorage.setItem(KEY, query ? `/work?${query}` : "/work");
  } catch {
    // storage blocked: Back falls back to /work
  }
}

export function WorkBackLink() {
  const [href, setHref] = useState("/work");
  useEffect(() => {
    try {
      const stored = window.sessionStorage.getItem(KEY);
      if (stored && (stored === "/work" || stored.startsWith("/work?"))) setHref(stored);
    } catch {
      // storage blocked: keep /work
    }
  }, []);
  return (
    <Link href={href} className="text-sm font-medium text-muted hover:text-ink">
      {href === "/work" ? "← All work" : "← Back to the work list"}
    </Link>
  );
}
