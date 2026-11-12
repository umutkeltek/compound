"use client";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useState } from "react";
import { PlusIcon } from "@/components/kit/icons";

export type NavApp = { slug: string; name: string; hue: string; stale: boolean; noData: boolean; kind: string };

const VISIBLE = 6;

/**
 * The sidebar's Apps group (the reference's "Projects"): a colour glyph per
 * app, ranked by the portfolio allocation. The dot says whether the app's data
 * is still arriving — amber means a feed stopped, which is worth seeing daily.
 */
export function NavAppLinks({ apps, total, note }: { apps: NavApp[]; total: number; note?: string }) {
  const pathname = usePathname();
  const [expanded, setExpanded] = useState(false);
  const shown = expanded ? apps : apps.slice(0, VISIBLE);
  return (
    <div>
      <div className="flex items-center justify-between px-2.5 pb-1.5 pt-1">
        <span className="text-xs font-medium text-faint">Apps</span>
        <Link href="/growth" className="text-faint transition-colors hover:text-ink" title="Growth audit for every app">
          <PlusIcon size={14} />
        </Link>
      </div>
      {note ? <div className="px-2.5 text-xs text-faint">{note}</div> : null}
      <div className="flex flex-col gap-0.5">
        {shown.map((app) => {
          const href = `/apps/${app.slug}`;
          const active = pathname === href;
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
      {total > VISIBLE ? (
        <button
          type="button"
          onClick={() => setExpanded((e) => !e)}
          className="mt-0.5 w-full rounded-lg px-2.5 py-1 text-left text-xs text-faint hover:text-muted"
        >
          {expanded ? "Show fewer" : `Show ${total - VISIBLE} more`}
        </button>
      ) : null}
    </div>
  );
}
