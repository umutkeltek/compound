"use client";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useState } from "react";
import { appHref } from "@/lib/links";

export type NavApp = { slug: string; name: string; hue: string; stale: boolean; noData: boolean; kind: string };

const VISIBLE = 6;

/**
 * The apps listed under the sidebar's Apps section: a colour glyph per app,
 * ranked by the portfolio allocation. The dot says whether the app's data is
 * still arriving; amber means a feed stopped. The app he is on always shows,
 * even when it ranks below the first six.
 */
export function visibleApps(apps: readonly NavApp[], currentSlug: string, expanded: boolean): NavApp[] {
  if (expanded) return [...apps];
  const shown = apps.slice(0, VISIBLE);
  const current = currentSlug ? apps.find((app) => app.slug === currentSlug) : undefined;
  if (current && !shown.includes(current)) shown.push(current);
  return shown;
}

/** The app slug in an /apps/<slug> address, or "". */
export function currentAppSlug(pathname: string): string {
  const match = /^\/apps\/([^/?#]+)/.exec(pathname);
  if (!match) return "";
  try {
    return decodeURIComponent(match[1]);
  } catch {
    return match[1];
  }
}

export function NavAppLinks({ apps, total, note }: { apps: NavApp[]; total: number; note?: string }) {
  const pathname = usePathname() ?? "";
  const [expanded, setExpanded] = useState(false);
  const current = currentAppSlug(pathname);
  const shown = visibleApps(apps, current, expanded);
  const hidden = total - shown.length;
  return (
    <div data-nav-apps>
      {note ? <div className="px-2.5 py-1 text-xs text-faint">{note}</div> : null}
      <div className="flex flex-col gap-0.5">
        {shown.map((app) => {
          const href = appHref(app.slug);
          const active = app.slug === current;
          return (
            <Link
              key={app.slug}
              href={href}
              className="nav-item"
              data-active={active}
              aria-current={active ? "page" : undefined}
              title={app.noData ? `${app.name}: no data source yet` : app.stale ? `${app.name}: a data feed stopped arriving` : app.name}
            >
              <span
                className="flex h-4.5 w-4.5 shrink-0 items-center justify-center rounded-full text-2xs font-bold text-white"
                style={{ backgroundColor: app.hue }}
                aria-hidden
              >
                {app.name.charAt(0)}
              </span>
              <span className="truncate">{app.name}</span>
              <span
                className={`ml-auto h-1.5 w-1.5 shrink-0 rounded-full ${app.noData ? "bg-hair" : app.stale ? "bg-seam" : "bg-good"}`}
                aria-label={app.noData ? "no data" : app.stale ? "data stale" : "data fresh"}
              />
            </Link>
          );
        })}
      </div>
      {apps.some((app) => app.stale) ? (
        <div className="flex items-center gap-1.5 px-2.5 pt-1 text-2xs text-faint" data-dot-legend>
          <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-seam" aria-hidden />
          Amber: a data feed stopped arriving
        </div>
      ) : null}
      {expanded || hidden > 0 ? (
        <button
          type="button"
          onClick={() => setExpanded((e) => !e)}
          className="mt-0.5 w-full rounded-lg px-2.5 py-1 text-left text-xs text-faint hover:text-muted"
        >
          {expanded ? "Show fewer" : `Show ${hidden} more`}
        </button>
      ) : null}
    </div>
  );
}
