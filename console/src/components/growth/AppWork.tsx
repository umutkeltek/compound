import Link from "next/link";

import { ReadFailedBanner, ReadNotice, type ReadView } from "@/components/kit/ReadState";
import { Panel } from "@/components/growth/GrowthKit";
import { NOT_MOVED_LABEL, type AppWorkModel } from "@/components/growth/app-work";

/** How many items the app page lists before "See all N in Work". */
export const APP_WORK_SHOWN = 8;

export type AppWorkReads = Array<{ label: string; read: ReadView }>;

function Count({ n, label, href, tone = "text-ink" }: { n: number; label: string; href: string; tone?: string }) {
  const text = `${n.toLocaleString("en-US")} ${label}`;
  if (!n) return <span className="text-faint">{text}</span>;
  return (
    <Link href={href} className={`font-medium hover:underline ${tone}`}>
      {text}
    </Link>
  );
}

function age(days: number | null): string {
  if (days === null) return "";
  if (days === 0) return "picked up today";
  return `picked up ${days} ${days === 1 ? "day" : "days"} ago`;
}

/**
 * "Work on this app": every open item the shared resolver places in this
 * app, on hold and not moved first. While the reads behind it have not
 * landed it says so (never "no work"); a failed refresh shows over the last
 * good copy.
 */
export function AppWork({ name, model, missing, reads }: { name: string; model: AppWorkModel | null; missing: AppWorkReads; reads: AppWorkReads }) {
  if (!model) {
    return (
      <Panel title="Work on this app" subtitle={`To find ${name}'s work, the page reads open work, your app list and where each app's code lives.`}>
        <div className="flex flex-col gap-2">
          {missing.map(({ label, read }) => (
            <ReadNotice key={label} read={read} label={label} />
          ))}
        </div>
      </Panel>
    );
  }
  const { counts, rows, hrefs } = model;
  const shown = rows.slice(0, APP_WORK_SHOWN);
  return (
    <Panel
      title="Work on this app"
      subtitle={
        counts.open ? (
          <span className="flex flex-wrap gap-x-1.5 tabular-nums">
            <Link href={hrefs.all} className="font-medium text-ink hover:underline">
              {counts.open.toLocaleString("en-US")} open {counts.open === 1 ? "work item" : "work items"}
            </Link>
            <span className="text-faint">·</span>
            <Count n={counts.needYou} label="on hold, waiting for you" href={hrefs.onHold} tone="text-seam" />
            <span className="text-faint">·</span>
            <Count n={counts.stale} label={NOT_MOVED_LABEL} href={hrefs.stale} tone="text-crit" />
            <span className="text-faint">·</span>
            <Count n={counts.waitingForCheck} label="waiting for a proof check" href={hrefs.checking} />
          </span>
        ) : (
          `No open work for ${name}.`
        )
      }
    >
      <div className="flex flex-col gap-3">
        {reads.map(({ label, read }) => (
          <ReadFailedBanner key={label} read={read} label={label} />
        ))}
        {shown.length ? (
          <ul className="divide-y divide-hair-2" data-app-work>
            {shown.map((row) => (
              <li key={row.id} className="flex items-start justify-between gap-4 py-2.5">
                <div className="min-w-0">
                  <Link href={row.href} className="text-sm font-medium text-ink hover:underline">
                    <span className="line-clamp-2">{row.title}</span>
                  </Link>
                  <span className="mono text-2xs text-faint">{row.id}</span>
                </div>
                <span className="shrink-0 text-right text-xs">
                  <span className={row.onHold ? "font-medium text-seam" : row.stale ? "font-medium text-crit" : "text-muted"}>
                    {row.onHold ? "On hold, waiting for you" : row.stale ? `Not moved · ${row.state}` : row.state}
                  </span>
                  {row.days !== null ? <span className="block text-faint">{age(row.days)}</span> : null}
                </span>
              </li>
            ))}
          </ul>
        ) : null}
        {rows.length > shown.length ? (
          <Link href={hrefs.all} className="self-start text-sm font-medium text-work hover:underline">
            See all {rows.length.toLocaleString("en-US")} in Work →
          </Link>
        ) : rows.length ? (
          <Link href={hrefs.all} className="self-start text-xs font-medium text-work hover:underline">
            Open these in Work →
          </Link>
        ) : null}
      </div>
    </Panel>
  );
}
