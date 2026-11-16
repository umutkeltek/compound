"use client";

import Link from "next/link";

import { EmptyState, ReadFailedBanner, ReadNotice } from "@/components/kit";
import type { ReadView } from "@/components/kit/ReadState";
import { appHref, workGroupHref, type WorkFilter } from "@/lib/links";
import { STALE_AFTER_DAYS } from "@/lib/work-state";

/**
 * Work grouped by app: one row per app, codebase and the "not linked" group
 * from the shared index (lib/compound.ts `readWorkProjectIndex`), in the
 * index's order (most items that need you first). Each count opens the List
 * of exactly those items in that group; the name opens all of them.
 */

export type WorkByAppGroup = {
  id: string;
  name: string;
  kind: string;
  open: number;
  needYou: number;
  inProgress: number;
  waitingForCheck: number;
  stale: number;
};

export type WorkByAppModel = {
  /** null until all three reads hold a usable list: unknown, never empty */
  groups: WorkByAppGroup[] | null;
  /** reads that hold no usable list yet, each with a plain label */
  missing: Array<{ label: string; read: ReadView }>;
  /** every read, for the "showing an older copy" banner */
  reads: Array<{ label: string; read: ReadView }>;
};

const KIND_WORDS: Record<string, string> = {
  app: "App",
  codebase: "Codebase",
  unlisted: "App not in your app list",
  none: "Names no app or codebase",
};

function Count({ n, label, group, filter, tone }: { n: number; label: string; group: WorkByAppGroup; filter: WorkFilter; tone?: string }) {
  const text = `${n.toLocaleString("en-US")} ${label}`;
  if (!n) return <span className="text-faint">{text}</span>;
  return (
    <Link href={workGroupHref(group, filter)} className={`font-medium hover:underline ${tone ?? "text-ink"}`}>
      {text}
    </Link>
  );
}

export function WorkByAppLens({ model }: { model: WorkByAppModel }) {
  const failed = model.reads.map(({ label, read }) => <ReadFailedBanner key={label} read={read} label={label} />);
  if (!model.groups) {
    return (
      <div className="flex flex-col gap-3">
        <p className="text-sm text-muted">To group work by app, the page reads the open work, your app list and where each app&apos;s code lives.</p>
        {model.missing.map(({ label, read }) => <ReadNotice key={label} read={read} label={label} />)}
      </div>
    );
  }
  const groups = model.groups;
  return (
    <div className="flex min-w-0 flex-col gap-3">
      {failed}
      <p className="text-sm text-muted">
        {groups.length.toLocaleString("en-US")} {groups.length === 1 ? "group" : "groups"} hold open work: your apps, other codebases, and work that names neither. The groups with the most items that need you come first.
      </p>
      {groups.length ? (
        <ul className="flex flex-col divide-y divide-hair overflow-hidden rounded-xl border border-hair bg-panel shadow-sm">
          {groups.map((group) => (
            <li key={group.id} className="flex flex-col gap-1.5 px-4 py-3 sm:flex-row sm:items-center sm:justify-between sm:gap-4">
              <div className="min-w-0">
                <div className="flex flex-wrap items-baseline gap-x-2">
                  <Link href={workGroupHref(group)} className="text-md font-semibold text-ink hover:underline">{group.name}</Link>
                  <span className="text-xs text-faint">{KIND_WORDS[group.kind] ?? group.kind}</span>
                  {group.kind === "app" ? (
                    <Link href={appHref(group.id)} className="text-xs font-medium text-work hover:underline">App page →</Link>
                  ) : null}
                </div>
                <Link href={workGroupHref(group)} className="text-xs text-muted hover:underline">
                  {group.open.toLocaleString("en-US")} open {group.open === 1 ? "work item" : "work items"}
                </Link>
              </div>
              <p className="flex flex-wrap gap-x-1.5 text-sm tabular-nums">
                <Count n={group.needYou} label="need you" group={group} filter="blocked" tone="text-seam" />
                <span className="text-faint">·</span>
                <Count n={group.inProgress} label="in progress" group={group} filter="progress" />
                <span className="text-faint">·</span>
                <Count n={group.waitingForCheck} label="waiting for a check" group={group} filter="verifying" />
                <span className="text-faint">·</span>
                <Count n={group.stale} label={`not moved in ${STALE_AFTER_DAYS === 7 ? "a week" : `${STALE_AFTER_DAYS} days`}`} group={group} filter="stale" tone="text-crit" />
              </p>
            </li>
          ))}
        </ul>
      ) : (
        <EmptyState>No open work.</EmptyState>
      )}
    </div>
  );
}
