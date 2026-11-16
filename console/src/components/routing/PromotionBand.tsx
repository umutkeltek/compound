"use client";

import Link from "next/link";

import { AuthorityFaceDetails } from "@/components/authority/AuthorityFaceDetails";
import { GovernedAct } from "@/components/decide/GovernedAct";
import { decideActIdentity } from "@/lib/governed-action-client";
import { ArrowUpIcon, ClockIcon, EmptyState, IdRef, SectionHeader, StateBadge } from "@/components/kit";
import type { DecideGroup } from "@/lib/decide-model";
import { decideHref } from "@/lib/links";

/**
 * Model changes waiting for a call. Each question links to the same decision
 * on Decide (`decideHref({ decision: key })`), which owns judging it and
 * re-checks its evidence when opened. A card whose owner offers a real action
 * keeps the shared governed control (GovernedAct, ruled by the console's
 * mutation readiness) after its owner's authority; a card with none shows no
 * command, only the link. Holds (evidence still thin) are one table, each row
 * linked to its decision, instead of a card per hold repeating one text.
 */

const ROUTE_TILE = "border-route-line bg-route-soft text-route";

function GlyphTile({ glyph }: { glyph: string }) {
  return (
    <span
      aria-hidden
      className={`inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-sm border text-xs font-bold ${ROUTE_TILE}`}
    >
      {glyph}
    </span>
  );
}

function ConsequencePair({ approve, hold }: { approve: string; hold: string }) {
  return (
    <div className="mt-2 rounded-md bg-soft px-3 py-2 text-xs leading-relaxed">
      <div className="text-ink">
        <span className="font-semibold text-good">if you act</span>
        <span className="text-faint"> → </span>
        {approve}
      </div>
      <div className="mt-0.5 text-muted">
        <span className="font-semibold">if you wait</span>
        <span> → </span>
        {hold}
      </div>
    </div>
  );
}

function RouteCard({
  group,
  seam,
  mutationsEnabled,
}: {
  group: DecideGroup;
  seam: boolean;
  mutationsEnabled: boolean;
}) {
  return (
    <article
      className={`rounded-lg border border-hair bg-panel px-4 py-3.5 shadow-sm transition-shadow hover:shadow ${
        seam ? "admission-seam" : ""
      }`}
    >
      <div className="flex items-start justify-between gap-2">
        <div className="flex min-w-0 items-start gap-2">
          <GlyphTile glyph={group.glyph} />
          <div className="min-w-0">
            <div className="flex items-center gap-1.5">
              <span className="text-2xs font-bold uppercase tracking-wide text-faint" title={group.areaPlain}>
                {group.areaLabel}
              </span>
              {group.count > 1 ? <span className="mono text-2xs tabular-nums text-faint">· {group.count}</span> : null}
            </div>
            <h3 className="mt-0.5 text-md font-semibold leading-snug text-ink">{group.title}</h3>
          </div>
        </div>
        <StateBadge state={seam ? "needs_you" : "watching"} label={seam ? "needs you" : "watching"} />
      </div>

      {group.capability.kind !== "unavailable" && group.unlock ? (
        <div className="mt-1 text-xs font-medium text-good">→ {group.unlock}</div>
      ) : null}
      {group.body ? (
        <p className="mt-2 text-sm leading-relaxed text-muted" style={{ maxWidth: "var(--read-max)" }}>
          {group.body}
        </p>
      ) : null}
      {group.consequences ? <ConsequencePair approve={group.consequences.approve} hold={group.consequences.hold} /> : null}

      {group.face.authority ? (
        <AuthorityFaceDetails authority={group.face.authority} embedded className="mt-3" />
      ) : null}

      <div className="mt-3 flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <span className={`text-xs tabular-nums ${group.old ? "font-semibold text-seam" : "text-faint"}`}>
            {group.ageLabel}
            {group.old ? " · waiting a long time" : ""}
          </span>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {group.act.mode !== "copy" && group.act.mode !== "batch-enumerable" ? (
            <GovernedAct
              key={decideActIdentity(group.act)}
              act={group.act}
              authority={group.face.authority}
              mutationsEnabled={mutationsEnabled}
              effectKind={group.capability.kind === "unavailable" ? "apply" : group.capability.kind}
            />
          ) : null}
          <Link href={decideHref({ decision: group.key })} className="rounded-md border border-work-line bg-work-soft px-3 py-1.5 text-xs font-semibold text-work hover:bg-work-soft/70">
            Decide this →
          </Link>
        </div>
      </div>

      {group.evidence.length ? (
        <details className="mt-3 rounded-md border border-hair bg-soft px-3 py-2 text-xs text-muted">
          <summary className="cursor-pointer font-medium text-muted">Evidence · {group.evidence.length}</summary>
          <ul className="mt-2 flex flex-col gap-0.5">
            {group.evidence.map((evidence, i) => (
              <li key={i} className="truncate text-2xs text-faint" title={evidence.ref}>
                {evidence.label}
              </li>
            ))}
          </ul>
        </details>
      ) : null}

      <div className="mt-2">
        <IdRef id={group.sampleId} kind="generic" truncate />
      </div>
    </article>
  );
}

export function PromotionBand({
  needsYou,
  holdRecord,
  classA,
  mutationsEnabled,
}: {
  needsYou: DecideGroup[];
  holdRecord: DecideGroup[];
  classA: boolean;
  mutationsEnabled: boolean;
}) {
  return (
    <section className="flex flex-col gap-4">
      <SectionHeader
        tone="route"
        icon={<ArrowUpIcon />}
        label="Model changes waiting for a call"
        right={
          <span className="text-xs tabular-nums text-faint">
            {needsYou.length
              ? `${needsYou.length} need${needsYou.length === 1 ? "s" : ""} your call`
              : "nothing needs your call"}
          </span>
        }
      />

      {classA ? (
        <p className="rounded-lg border border-crit-line bg-crit-soft px-4 py-3 text-sm leading-relaxed text-crit">
          One of these changes cannot be undone once made. Its decision on Decide shows the evidence; read it there first.
        </p>
      ) : null}

      {needsYou.length ? (
        <div className="flex flex-col gap-3">
          {needsYou.map((group) => (
            <RouteCard key={group.key} group={group} seam mutationsEnabled={mutationsEnabled} />
          ))}
        </div>
      ) : (
        <EmptyState>No model change waits for your call.</EmptyState>
      )}

      {holdRecord.length ? (
        <div className="flex flex-col gap-3">
          <SectionHeader
            tone="neutral"
            icon={<ClockIcon />}
            label="Waiting for more evidence"
            right={<span className="mono text-2xs tabular-nums text-faint">{holdRecord.length}</span>}
          />
          <p className="text-sm leading-relaxed text-muted">
            These model changes do not have enough runs behind them to judge yet. Nothing changes while they wait; each
            one is on Decide if you want to look.
          </p>
          <div className="overflow-x-auto rounded-lg border border-hair bg-panel">
            <table className="w-full text-left text-sm">
              <thead>
                <tr className="border-b border-hair text-2xs uppercase tracking-wide text-faint">
                  <th className="px-3 py-2 font-semibold">Model change</th>
                  <th className="px-3 py-2 font-semibold">Waiting</th>
                  <th className="px-3 py-2 font-semibold"><span className="sr-only">Decision</span></th>
                </tr>
              </thead>
              <tbody>
                {holdRecord.map((group) => (
                  <tr key={group.key} className="border-b border-hair last:border-b-0">
                    <td className="px-3 py-2 text-ink">{group.title}</td>
                    <td className={`whitespace-nowrap px-3 py-2 text-2xs ${group.old ? "text-seam" : "text-muted"}`}>{group.ageLabel}</td>
                    <td className="px-3 py-2 text-right"><Link href={decideHref({ decision: group.key })} className="text-xs font-semibold text-work hover:underline">Open on Decide →</Link></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      ) : null}
    </section>
  );
}
