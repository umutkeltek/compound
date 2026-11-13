"use client";
import { usePathname, useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import { surfaceForPath } from "@/lib/experience";

type Depth = "glance" | "understand" | "investigate" | "technical";

const DEPTHS: Array<{ id: Depth; label: string }> = [
  { id: "glance", label: "Glance" },
  { id: "understand", label: "Understand" },
  { id: "investigate", label: "Investigate" },
  { id: "technical", label: "Technical" },
];

const DEPTH_KEY = "compound-experience-depth";

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
  const surface = surfaceForPath(pathname);
  const progressive = surface.id === "today" || surface.id === "live";
  const [depth, setDepth] = useState<Depth>("understand");

  useEffect(() => {
    const saved = window.localStorage.getItem(DEPTH_KEY) as Depth | null;
    const resolved = DEPTHS.some((item) => item.id === saved) ? saved! : "understand";
    setDepth(resolved);
    document.documentElement.dataset.depth = resolved;
    if (cookieDepth() !== resolved) {
      writeDepthCookie(resolved);
      if (resolved === "technical") router.refresh();
    }
  }, [router]);

  function choose(next: Depth) {
    const crossesTechnical = (depth === "technical") !== (next === "technical");
    setDepth(next);
    window.localStorage.setItem(DEPTH_KEY, next);
    writeDepthCookie(next);
    document.documentElement.dataset.depth = next;
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
              <span className={item.id === "investigate" || item.id === "technical" ? "hidden sm:inline" : ""}>{item.label}</span>
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}
