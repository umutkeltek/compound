"use client";

import { useEffect } from "react";

/**
 * Groups on the app page are closed `<details>` by default. A link such as
 * `/apps/x#area-acquisition_search` must land on an open group, so this opens
 * every `<details>` around the hash target (and the target itself when it is a
 * `<details>`), then scrolls to it. No state is kept; the page stays server-rendered.
 */
export function OpenOnHash() {
  useEffect(() => {
    function reveal() {
      const id = decodeURIComponent(window.location.hash.slice(1));
      if (!id) return;
      const target = document.getElementById(id);
      if (!target) return;
      let node: HTMLElement | null = target;
      while (node) {
        if (node instanceof HTMLDetailsElement) node.open = true;
        node = node.parentElement;
      }
      target.scrollIntoView({ block: "start" });
    }
    reveal();
    window.addEventListener("hashchange", reveal);
    return () => window.removeEventListener("hashchange", reveal);
  }, []);
  return null;
}
