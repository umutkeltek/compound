"use client";
import { usePathname } from "next/navigation";
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

/**
 * Information depth for the two progressive surfaces (Today, Live). Every
 * other page renders nothing here: sub-pages are in the sidebar now, so this
 * bar no longer repeats them as tabs. Depth changes presentation only;
 * projection reads never depend on it.
 */
export function ExperienceBar() {
  const pathname = usePathname();
  const surface = surfaceForPath(pathname);
  const progressive = surface.id === "today" || surface.id === "live";
  const [depth, setDepth] = useState<Depth>("understand");

  useEffect(() => {
    const saved = window.localStorage.getItem(DEPTH_KEY) as Depth | null;
    const resolved = DEPTHS.some((item) => item.id === saved) ? saved! : "understand";
    setDepth(resolved);
    document.documentElement.dataset.depth = resolved;
  }, []);

  function choose(next: Depth) {
    setDepth(next);
    window.localStorage.setItem(DEPTH_KEY, next);
    document.documentElement.dataset.depth = next;
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
