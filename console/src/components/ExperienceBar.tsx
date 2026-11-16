"use client";
import { usePathname, useRouter } from "next/navigation";
import { useEffect, useState } from "react";

type Depth = "glance" | "understand" | "investigate" | "technical";

const DEPTHS: Array<{ id: Depth; label: string }> = [
  { id: "glance", label: "Glance" },
  { id: "understand", label: "Understand" },
  { id: "investigate", label: "Investigate" },
  { id: "technical", label: "Technical" },
];

const DEPTH_KEY = "compound-experience-depth";

/** The only pages with a depth control; depth hides nothing anywhere else. */
export const DEPTH_PAGES: readonly string[] = ["/", "/live"];

/** The depth attribute for <html> on this page: the chosen depth on Today and Live, none elsewhere. */
export function depthAttributeFor(pathname: string, depth: string): string | null {
  return DEPTH_PAGES.includes(pathname) ? depth : null;
}

function applyDepth(pathname: string, depth: Depth) {
  const value = depthAttributeFor(pathname, depth);
  if (value) document.documentElement.dataset.depth = value;
  else delete document.documentElement.dataset.depth;
}

function cookieDepth(): string {
  const match = document.cookie.split("; ").find((part) => part.startsWith(`${DEPTH_KEY}=`));
  return match ? decodeURIComponent(match.slice(DEPTH_KEY.length + 1)) : "";
}

/** Mirror the preference into a cookie so the server can skip the reads of
 * sections this depth hides (lib/depth.ts). */
function writeDepthCookie(depth: Depth) {
  document.cookie = `${DEPTH_KEY}=${encodeURIComponent(depth)}; path=/; max-age=31536000; samesite=lax`;
}

/**
 * Information depth for the two progressive surfaces (Today, Live). Every
 * other page renders nothing here: sub-pages are in the sidebar now, so this
 * bar no longer repeats them as tabs. Depth changes presentation; the only
 * read it changes is that sections hidden at this depth skip their reads
 * (the server sees the depth through a cookie), so a change re-renders.
 */
export function ExperienceBar() {
  const pathname = usePathname();
  const router = useRouter();
  const progressive = DEPTH_PAGES.includes(pathname);
  const [depth, setDepth] = useState<Depth>("understand");
  // The saved depth is read after mount; until then the first-paint script's attribute stands.
  const [ready, setReady] = useState(false);

  useEffect(() => {
    const saved = window.localStorage.getItem(DEPTH_KEY) as Depth | null;
    const resolved = DEPTHS.some((item) => item.id === saved) ? saved! : "understand";
    setDepth(resolved);
    setReady(true);
    if (cookieDepth() !== resolved) {
      writeDepthCookie(resolved);
      if (resolved === "technical") router.refresh();
    }
  }, [router]);

  // A depth chosen on Today or Live never hides a section on another page.
  useEffect(() => {
    if (ready || !DEPTH_PAGES.includes(pathname)) applyDepth(pathname, depth);
  }, [pathname, depth, ready]);

  function choose(next: Depth) {
    const crossesTechnical = (depth === "technical") !== (next === "technical");
    setDepth(next);
    window.localStorage.setItem(DEPTH_KEY, next);
    writeDepthCookie(next);
    applyDepth(pathname, next);
    if (crossesTechnical) router.refresh();
  }

  if (!progressive) return null;

  return (
    <div className="px-4 pt-4 sm:px-6 md:px-8">
      <div className="mx-auto flex w-full justify-end" style={{ maxWidth: "1480px" }}>
        <div className="flex shrink-0 items-center gap-0.5 rounded-lg border border-hair bg-soft p-0.5" aria-label="Information depth">
          {DEPTHS.map((item) => (
            <button
              key={item.id}
              type="button"
              aria-pressed={depth === item.id}
              onClick={() => choose(item.id)}
              className="rounded-md px-2.5 py-1 text-xs font-medium text-faint transition-colors hover:text-ink aria-pressed:bg-panel aria-pressed:text-ink aria-pressed:shadow-sm"
            >
              <span>{item.label}</span>
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}
