import Link from "next/link";

import { arr, num, str, type Json } from "@/lib/compound";
import { appHref } from "@/lib/links";

import { GavelIcon, StatusChip } from "@/components/kit";

import { Card, CountChip, DoctrineNote, FileOwnedFoot, ForAgents, KV, SettingsSection } from "./primitives";

/** What the write guard does in each mode, in words. */
const GUARD_MODE: Record<string, string> = {
  observe: "Writes to protected places are noted, not stopped.",
  warn: "Writes to protected places go ahead with a warning.",
  block: "Writes to protected places are refused.",
};

/**
 * Group 2 — Operating rules, each card in one plain sentence: what it
 * controls, its value now, and that it changes only in its file. Raw keys
 * sit behind "For agents". Effective placement mode + approval frontier + operator
 * prompts + release-goal holds, all read-only. Each card is footed with the
 * exact `compound policy import-…` command: these are file-owned authorities,
 * so the console shows what is in effect and never edits them.
 */
export function PolicyGroup({
  placement,
  frontier,
  prompts,
  holds,
  decisions,
  generatedAt,
}: {
  placement: Json | null;
  frontier: Json | null;
  prompts: Json | null;
  holds: Json | null;
  decisions: Json | null;
  generatedAt?: string;
}) {
  const placementMode = str(placement?.placement_guard_mode) || "—";
  const blockOwners = arr(placement?.block_owner_ids).map(String);
  const openWrites = num(placement?.open_write_count);
  const ownerCount = num(placement?.owner_count);

  const familyCount = num(frontier?.family_count);
  const deferredCount = num(frontier?.deferred_count);
  const openApprovals = num(frontier?.approval_required_open_count);
  const families = arr(frontier?.families);

  const promptCount = num(prompts?.prompt_count);
  const highSeverity = num(prompts?.high_severity_count);
  const promptRows = arr(prompts?.prompts);

  const holdCount = num(holds?.hold_count);
  const holdRows = arr(holds?.holds);

  const decisionRecords = num(decisions?.record_count);
  const decisionsByStatus = (decisions?.counts_by_status ?? {}) as Record<string, number>;
  const decisionsByKind = (decisions?.counts_by_kind ?? {}) as Record<string, number>;
  const decisionPrimary = num(decisionsByStatus.compound_primary);
  const decisionParked = num(decisionsByStatus.compound_parked);

  return (
    <SettingsSection
      index={2}
      icon={<GavelIcon />}
      tone="route"
      title="Operating rules"
      question="What agents may not do, what needs your approval, and what is on hold."
      source="compound policy placement-authority --json"
      generatedAt={generatedAt}
    >
      <div className="grid gap-4 md:grid-cols-2">
        <Card title="Where agents may write" badge={placement === null ? <StatusChip tone="muted" label="not read" /> : <CountChip n={blockOwners.length} label="refused" tone="muted" />}>
          <p className="text-sm leading-relaxed text-ink">
            {placement === null
              ? "The write guard could not be read."
              : `${GUARD_MODE[placementMode] ?? `The write guard is set to "${placementMode}".`} ${blockOwners.length ? `${blockOwners.length.toLocaleString("en-US")} protected ${blockOwners.length === 1 ? "place refuses" : "places refuse"} every write.` : ""}`}
          </p>
          <FileOwnedFoot cmd="compound policy placement-authority --json" note="Set in the placement rules file; nothing in this console changes it." />
          <ForAgents label="For agents: technical record">
            <KV k="placement_guard_mode" v={placementMode} />
            <KV k="owner_count" v={ownerCount} />
            <KV k="open_write_count" v={openWrites} />
            <KV k="block owners" v={blockOwners.length ? blockOwners.join(", ") : "—"} mono={false} />
          </ForAgents>
        </Card>

        <Card title="What needs your approval" badge={<CountChip n={openApprovals} label="open" tone={openApprovals > 0 ? "seam" : "good"} />}>
          <p className="text-sm leading-relaxed text-ink">
            {frontier === null
              ? "Which actions need your approval could not be read."
              : `${familyCount.toLocaleString("en-US")} ${familyCount === 1 ? "kind of action needs" : "kinds of action need"} your approval before an agent does it; ${openApprovals === 0 ? "none is waiting now" : `${openApprovals.toLocaleString("en-US")} ${openApprovals === 1 ? "is" : "are"} waiting`}${deferredCount ? `, and ${deferredCount.toLocaleString("en-US")} ${deferredCount === 1 ? "is" : "are"} put off for later` : ""}.`}
          </p>
          <FileOwnedFoot cmd="compound policy operator-approval-frontier-authority --json" note="Set in the approval rules file; nothing in this console changes it." />
          {families.length ? (
            <ForAgents label="For agents: technical record">
              {families.map((family: Json, index: number) => (
                <KV key={str(family.id) || index} k={str(family.id) || "—"} v={str(family.current_decision) || "—"} />
              ))}
            </ForAgents>
          ) : null}
        </Card>

        <Card title="Questions for you" badge={<CountChip n={promptCount} label="open" tone={highSeverity > 0 ? "seam" : "muted"} />}>
          {prompts === null ? (
            <DoctrineNote>The open questions could not be read.</DoctrineNote>
          ) : promptRows.length ? (
            <>
              <ul className="flex flex-col gap-2">
                {promptRows.map((prompt: Json, index: number) => (
                  <li key={str(prompt.id) || index} className="text-sm leading-relaxed text-ink">
                    {str(prompt.question) || str(prompt.topic) || "A question with no text"}
                    {str(prompt.severity).toLowerCase() === "high" ? <span className="ml-2 text-2xs font-semibold text-seam">important</span> : null}
                  </li>
                ))}
              </ul>
              <p className="mt-2 text-2xs leading-relaxed text-faint">
                No page in the console answers these yet; an agent asks you when it reaches one.
              </p>
            </>
          ) : (
            <DoctrineNote>No open questions.</DoctrineNote>
          )}
          <FileOwnedFoot cmd="compound policy operator-prompts-authority --json" note="Kept in the questions file; nothing in this console changes it." />
        </Card>

        <Card title="Releases on hold" badge={<CountChip n={holdCount} label="on hold" tone={holdCount > 0 ? "seam" : "good"} />}>
          {holds === null ? (
            <DoctrineNote>The release holds could not be read.</DoctrineNote>
          ) : holdRows.length ? (
            <>
              <p className="mb-2 text-sm leading-relaxed text-ink">
                {holdCount.toLocaleString("en-US")} {holdCount === 1 ? "release waits" : "releases wait"} for evidence before it can go ahead.
              </p>
              <ul className="flex flex-col gap-1.5">
                {holdRows.map((hold: Json, index: number) => {
                  const app = str(hold.app_id);
                  const label = str(hold.objective) || app || str(hold.goal_id) || "Release";
                  return (
                    <li key={str(hold.goal_id) || index} className="flex items-baseline justify-between gap-2 text-sm">
                      {app ? <Link href={appHref(app)} className="min-w-0 truncate text-ink hover:underline" title={label}>{label}</Link> : <span className="min-w-0 truncate text-ink" title={label}>{label}</span>}
                      <span className="mono shrink-0 text-2xs text-faint">{app || str(hold.goal_id)}</span>
                    </li>
                  );
                })}
              </ul>
              {holdCount > holdRows.length ? <p className="mt-1 text-2xs text-faint">{(holdCount - holdRows.length).toLocaleString("en-US")} more not listed by the holds file.</p> : null}
            </>
          ) : (
            <DoctrineNote>No release is on hold.</DoctrineNote>
          )}
          <FileOwnedFoot cmd="compound policy release-goal-holds-authority --json" note="Kept in the release holds file; nothing in this console changes it." />
        </Card>

        <Card title="Decisions on file" badge={<CountChip n={decisionRecords} label="on file" tone="muted" />}>
          <p className="text-sm leading-relaxed text-ink">
            {decisions === null
              ? "The decisions on file could not be read."
              : `${decisionRecords.toLocaleString("en-US")} of your decisions are on file: ${decisionPrimary.toLocaleString("en-US")} in force, ${decisionParked.toLocaleString("en-US")} set aside.`}
          </p>
          <FileOwnedFoot cmd="compound decisions authority --json" note="Added by importing your decision notes; nothing in this console changes them." />
          <ForAgents label="For agents: technical record">
            <KV k="by kind" v={Object.entries(decisionsByKind).map(([kind, count]) => `${kind}:${count}`).join("  ") || "—"} />
          </ForAgents>
        </Card>
      </div>
    </SettingsSection>
  );
}
