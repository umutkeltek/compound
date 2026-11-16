import type { ReactNode } from "react";
import { CopyButton } from "@/components/growth/CopyButton";
import { hasPlaceholder } from "@/lib/ads";
import { GrowthStatusChip, StatusMixBar } from "@/components/growth/GrowthKit";
import {
  DIMENSION_META,
  MOVE_KIND_LABEL,
  PROFILE_DIMENSIONS,
  STATUS_META,
  branchLabel,
  countStatuses,
  hasEvidence,
  isJudged,
  joinWords,
  sharedMoves,
  unknownGates,
  valueLabel,
  type AuditRow,
  type GrowthApp,
  type GrowthArea,
  type GrowthGroup,
  type NextMove,
  type ProfileDimension,
} from "@/lib/growth";

/*
 * The app page's growth blocks: next moves first, then the profile that
 * decides what applies, then the 12 groups. Every status, order and count is
 * the CLI's; these components only choose words and weight.
 */

type Lookup = { areaById: Map<string, GrowthArea>; rowById: Map<string, AuditRow> };

export function makeLookup(app: GrowthApp, areas: GrowthArea[]): Lookup {
  return { areaById: new Map(areas.map((a) => [a.id, a])), rowById: new Map(app.audit.map((r) => [r.areaId, r])) };
}

/** An area's title for this app, in quotes: titles contain "and", so a bare list of them is ambiguous. */
function areaTitle(id: string, look: Lookup): string {
  return `“${look.rowById.get(id)?.title || look.areaById.get(id)?.title || id}”`;
}

/**
 * Areas that wait on `areaId` (directly or through another area) and still
 * apply to this app. Used only to NAME what a move unblocks; the count shown
 * is always the CLI's `unblocks`.
 */
function dependents(areaId: string, look: Lookup): string[] {
  const out = new Set<string>();
  const queue = [areaId];
  while (queue.length) {
    const current = queue.shift()!;
    for (const area of look.areaById.values()) {
      if (out.has(area.id) || area.id === areaId || !area.needs.includes(current)) continue;
      const st = look.rowById.get(area.id)?.status;
      if (st === "not_applicable" || st === "deferred") continue;
      out.add(area.id);
      queue.push(area.id);
    }
  }
  return [...out];
}

function Dot() {
  return <span className="text-faint" aria-hidden>·</span>;
}

function MoveMeta({ move }: { move: NextMove }) {
  // The kind is not repeated here: the CLI's titles already lead with it
  // ("Connect data: …", "Record the answer: …"). It stays on the number badge's tooltip.
  // Workflow and skill names are for agents: they sit in the move's "For agents" disclosure.
  const parts: ReactNode[] = [];
  if (move.areaIds.length > 1) {
    parts.push(<span key="areas">Covers {move.areaIds.length} areas</span>);
  } else if (move.areaId) {
    parts.push(<a key="area" href={`#area-${move.areaId}`} className="hover:text-ink hover:underline">See this area</a>);
  }
  return (
    <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-2xs text-faint">
      {parts.map((p, i) => (
        <span key={i} className="inline-flex items-center gap-2">
          {i ? <Dot /> : null}
          {p}
        </span>
      ))}
    </div>
  );
}

function UnblockLine({ move, look }: { move: NextMove; look: Lookup }) {
  if (move.blockedBy.length) {
    return (
      <p className="text-sm text-ink">
        <span className="font-medium">Needs {joinWords(move.blockedBy.map((id) => areaTitle(id, look)))} first.</span>
      </p>
    );
  }
  if (move.unblocks <= 0 || !move.areaId) return null;
  const names = dependents(move.areaId, look).map((id) => areaTitle(id, look));
  // Name them when the list is short and matches the CLI's count; otherwise name three and count the rest.
  const shown = names.length === move.unblocks ? (names.length <= 4 ? names : [...names.slice(0, 3), `${names.length - 3} more`]) : [];
  return (
    <p className="text-sm text-ink" title={names.length === move.unblocks ? names.join(", ") : undefined}>
      <span className="font-medium">Unblocks </span>
      {shown.length ? `${joinWords(shown)}.` : `${move.unblocks} other ${move.unblocks === 1 ? "area" : "areas"}.`}
    </p>
  );
}

/** What an agent needs to start a move: its workflow, skills and read command. Never a command with a placeholder. */
function ForAgents({ move }: { move: NextMove }) {
  const lines = [
    move.branch ? `Workflow: ${branchLabel(move.branch)}` : "",
    move.skills.length ? `Skills: ${move.skills.join(", ")}` : "",
  ].filter(Boolean);
  const command = move.brief && !hasPlaceholder(move.brief) ? move.brief : "";
  if (!lines.length && !command) return null;
  return (
    <details className="text-2xs text-faint">
      <summary className="cursor-pointer">For agents</summary>
      <div className="mt-1 flex flex-col gap-1">
        {lines.map((line) => <span key={line}>{line}</span>)}
        {command ? <CopyButton text={command} label="Copy the read command" /> : null}
      </div>
    </details>
  );
}

/** The hero list: at most five moves, in the CLI's order. */
export function NextMoves({ app, look }: { app: GrowthApp; look: Lookup }) {
  const moves = app.nextMoves.slice(0, 5);
  if (!moves.length) {
    return <p className="rounded-xl border border-dashed border-hair bg-page px-4 py-3 text-sm text-muted">No open move. Every area that applies is measured.</p>;
  }
  return (
    <ol className="flex flex-col divide-y divide-hair-2 rounded-2xl border border-hair bg-panel">
      {moves.map((move, i) => (
        <li key={`${move.kind}-${move.areaId}-${i}`} className="flex gap-4 p-4">
          <span
            className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-strong text-xs font-semibold text-panel tabular-nums"
            title={MOVE_KIND_LABEL[move.kind] ?? move.kind}
          >
            {i + 1}
          </span>
          <div className="flex min-w-0 flex-1 flex-col gap-1">
            <h3 className="min-w-0 text-md font-semibold text-ink">{move.title}</h3>
            <p className="line-clamp-2 max-w-3xl text-sm text-muted" title={move.why}>{move.why}</p>
            <UnblockLine move={move} look={look} />
            <MoveMeta move={move} />
            <ForAgents move={move} />
          </div>
        </li>
      ))}
    </ol>
  );
}

function ProfileChips({ dim, app }: { dim: ProfileDimension; app: GrowthApp }) {
  const value = app.profile[dim];
  const missing = value.source === "unknown" || value.values.length === 0;
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <span className="text-2xs text-faint">{DIMENSION_META[dim].label}</span>
      <div className="flex flex-wrap gap-1">
        {missing ? (
          <span className="inline-flex items-center rounded-md border border-dashed border-hair px-2 py-0.5 text-xs text-faint" title={`${DIMENSION_META[dim].label} is not in the product contract, and the data does not show it.`}>
            Not on file
          </span>
        ) : (
          value.values.map((v) => (
            <span
              key={v}
              className="inline-flex items-center gap-1 rounded-md border border-hair bg-soft px-2 py-0.5 text-xs text-ink"
              title={value.source === "inferred" ? "Guessed from the data Compound holds. Write it in the product contract to be sure." : "Written in the product contract."}
            >
              {valueLabel(v)}
              {value.source === "inferred" ? <span className="text-2xs text-faint">from data</span> : null}
            </span>
          ))
        )}
      </div>
    </div>
  );
}

/** One plain sentence: what filling in the missing profile would change. */
export function profileSentence(app: GrowthApp, areas: GrowthArea[]): string {
  const gates = unknownGates(app, areas);
  if (gates.rows > 0) {
    const what = gates.dims.length ? joinWords(gates.dims.map((d) => DIMENSION_META[d].phrase)) : "the profile";
    const verb = gates.dims.length > 1 ? "are" : "is";
    return `${gates.rows} ${gates.rows === 1 ? "area" : "areas"} cannot be judged until ${what} ${verb} on file.`;
  }
  const missing = app.profile.unknown;
  if (!missing.length) return "";
  const names = joinWords(missing.map((d) => DIMENSION_META[d].label.toLowerCase()));
  const first = names.charAt(0).toUpperCase() + names.slice(1);
  return `${first} ${missing.length > 1 ? "are" : "is"} not on file. No area waits on ${missing.length > 1 ? "them" : "it"} today.`;
}

export function ProfileRow({ app, areas, groups }: { app: GrowthApp; areas: GrowthArea[]; groups: GrowthGroup[] }) {
  const sentence = profileSentence(app, areas);
  const titleOf = (id: string) => groups.find((g) => g.id === id)?.title ?? areas.find((a) => a.id === id)?.title ?? id;
  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h2 className="text-md font-semibold text-ink">What the product is</h2>
        <span className="text-xs text-faint">Decides which areas apply</span>
      </div>
      <div className="grid grid-cols-2 gap-x-6 gap-y-3 sm:grid-cols-3 xl:grid-cols-5">
        {PROFILE_DIMENSIONS.map((dim) => (
          <ProfileChips key={dim} dim={dim} app={app} />
        ))}
      </div>
      {sentence ? (
        <p className="text-sm text-ink">
          {sentence}{" "}
          {app.profile.unknown.length ? (
            <span className="text-muted">
              Nothing fills it in on its own yet: it is written in the app&apos;s product description file, which an agent can update when it works on this app.
            </span>
          ) : null}
        </p>
      ) : null}
      {app.profile.deferred.length ? (
        <ul className="flex flex-col gap-0.5 text-sm text-muted">
          {app.profile.deferred.map((d) => (
            <li key={d.area}>
              <span className="font-medium text-ink">Set aside: {titleOf(d.area)}</span>
              {d.until ? ` until ${d.until}` : ""}
              {d.reason ? `. ${d.reason}` : ""}
            </li>
          ))}
        </ul>
      ) : null}
      {app.essence && (app.essence.audience || app.essence.problem || app.essence.value) ? (
        <details className="group text-sm">
          <summary className="cursor-pointer list-none text-xs font-medium text-muted hover:text-ink">
            <span className="inline-block transition-transform group-open:rotate-90" aria-hidden>›</span> Who it is for, the problem and the value
          </summary>
          <dl className="mt-2 grid max-w-4xl grid-cols-1 gap-3 md:grid-cols-3">
            {(
              [
                ["Audience", app.essence.audience],
                ["Problem", app.essence.problem],
                ["Value", app.essence.value],
              ] as const
            )
              .filter(([, v]) => v)
              .map(([k, v]) => (
                <div key={k}>
                  <dt className="text-2xs text-faint">{k}</dt>
                  <dd className="line-clamp-4 text-ink" title={v}>
                    {v}
                  </dd>
                </div>
              ))}
          </dl>
        </details>
      ) : null}
    </section>
  );
}

function groupLine(counts: ReturnType<typeof countStatuses>): string {
  const judged = counts.measured + counts.partial + counts.stale + counts.blind;
  if (judged > 0) {
    const known = counts.measured + counts.partial + counts.stale;
    if (counts.measured > 0) return `${counts.measured} of ${judged} measured`;
    if (known > 0) return `${known} of ${judged} with some data`;
    return `No data yet in ${judged === 1 ? "the 1 area" : `all ${judged}`}`;
  }
  if (counts.unknown) return "Not on file";
  if (counts.deferred) return "Set aside";
  return "Does not apply";
}

function AreaRow({ row, area, look, moveRank }: { row: AuditRow | undefined; area: GrowthArea; look: Lookup; moveRank: number }) {
  const status = row?.status ?? "blind";
  const title = row?.title || area.title;
  const question = row?.question || area.question;
  const forms = row?.forms ?? [];
  const judged = isJudged(status);
  const text = row?.summary || row?.reason || STATUS_META[status].meaning;
  return (
    <li id={`area-${area.id}`} className="grid scroll-mt-24 grid-cols-1 gap-x-6 gap-y-1.5 px-4 py-3 target:bg-work-soft md:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <GrowthStatusChip status={status} />
          <span className={`font-medium ${judged ? "text-ink" : "text-muted"}`}>{title}</span>
          {moveRank > 0 ? (
            <span className="inline-flex items-center gap-1 rounded-md border border-strong px-1.5 py-0.5 text-2xs font-medium text-ink">Next move {moveRank}</span>
          ) : null}
        </div>
        <p className="mt-0.5 text-xs text-muted">{question}</p>
        {forms.length > 1 ? <p className="mt-0.5 text-2xs text-faint">Checked as: {forms.join(" · ")}</p> : null}
        <p className="mono mt-0.5 text-2xs text-faint">{area.id}</p>
      </div>
      <div className="min-w-0 text-sm">
        <p className={judged && status !== "blind" ? "text-ink" : "text-muted"}>{text}</p>
        {row?.blockedBy.length ? (
          <p className="mt-0.5 font-medium text-ink">Needs {joinWords(row.blockedBy.map((id) => areaTitle(id, look)))} first.</p>
        ) : null}
        {row?.nextMove && judged && row.nextMove !== text ? <p className="mt-0.5 text-muted">Next: {row.nextMove}</p> : null}

      </div>
    </li>
  );
}

/**
 * One group's areas. Areas with any data, and areas that hold a next move,
 * come first. The rest (no data yet, not on file, set aside, does not apply)
 * fold into one quiet line that opens on demand, so an open group never
 * becomes a wall of gaps. A group with nothing but gaps shows them directly.
 */
function GroupRows({ areas, look, moveRank }: { areas: GrowthArea[]; look: Lookup; moveRank: Map<string, number> }) {
  const statusOf = (a: GrowthArea) => look.rowById.get(a.id)?.status ?? "blind";
  const lead = areas.filter((a) => moveRank.has(a.id) || hasEvidence(statusOf(a)));
  const quiet = areas.filter((a) => !lead.includes(a));
  const row = (area: GrowthArea) => <AreaRow key={area.id} row={look.rowById.get(area.id)} area={area} look={look} moveRank={moveRank.get(area.id) ?? 0} />;
  if (!lead.length || quiet.length <= 1) {
    return <ul className="divide-y divide-hair-2 border-t border-hair">{areas.map(row)}</ul>;
  }
  const counts = countStatuses(quiet.map((a) => ({ status: statusOf(a) })));
  const label = (["blind", "unknown", "deferred", "not_applicable"] as const)
    .filter((st) => counts[st] > 0)
    .map((st) => `${counts[st]} ${STATUS_META[st].label.toLowerCase()}`)
    .join(" · ");
  return (
    <div className="border-t border-hair">
      <ul className="divide-y divide-hair-2">{lead.map(row)}</ul>
      <details className="group/quiet border-t border-hair-2">
        <summary className="flex cursor-pointer list-none items-baseline gap-2 px-4 py-2.5 text-sm hover:bg-soft">
          <span className="inline-block w-3 shrink-0 text-faint transition-transform group-open/quiet:rotate-90" aria-hidden>›</span>
          <span className="shrink-0 text-muted">{quiet.length} more: {label}</span>
          <span className="hidden min-w-0 truncate text-xs text-faint md:inline">{quiet.map((a) => look.rowById.get(a.id)?.title || a.title).join(", ")}</span>
        </summary>
        <ul className="divide-y divide-hair-2 border-t border-hair-2">{quiet.map(row)}</ul>
      </details>
    </div>
  );
}

/** The 12 groups as closed sections; a group holding an area a next move covers starts open. */
export function GroupAudit({ app, groups, areas, look }: { app: GrowthApp; groups: GrowthGroup[]; areas: GrowthArea[]; look: Lookup }) {
  // Every area a move covers carries its rank: a folded refresh marks all of its areas, not only the first.
  const moveRank = new Map<string, number>();
  app.nextMoves.slice(0, 5).forEach((m, i) => {
    for (const id of m.areaIds) if (!moveRank.has(id)) moveRank.set(id, i + 1);
  });
  return (
    <div className="flex flex-col gap-2">
      {groups.map((group) => {
        const groupAreas = areas.filter((a) => a.group === group.id);
        const rows = groupAreas.map((a) => look.rowById.get(a.id)).filter((r): r is AuditRow => Boolean(r));
        const counts = countStatuses(rows);
        // Count moves, not areas: one folded refresh with two areas here is one move.
        const moves = new Set(groupAreas.filter((a) => moveRank.has(a.id)).map((a) => moveRank.get(a.id))).size;
        return (
          <details key={group.id} id={`group-${group.id}`} open={moves > 0} className="group scroll-mt-24 rounded-xl border border-hair bg-panel target:border-work-line">
            <summary className="flex cursor-pointer list-none flex-wrap items-center gap-x-4 gap-y-1.5 px-4 py-3 hover:bg-soft">
              <span className="flex min-w-0 flex-1 items-baseline gap-2">
                <span className="inline-block w-3 shrink-0 text-faint transition-transform group-open:rotate-90" aria-hidden>›</span>
                <span className="shrink-0 font-semibold text-ink">{group.title}</span>
                <span className="hidden min-w-0 truncate text-sm text-muted md:inline">{group.question}</span>
              </span>
              <span className="flex shrink-0 items-center gap-3 pl-5 md:pl-0">
                {moves ? (
                  <span className="inline-flex items-center gap-1.5 rounded-md border border-strong px-1.5 py-0.5 text-2xs font-medium text-ink">
                    <span className="h-1.5 w-1.5 rounded-full bg-ink" aria-hidden />
                    {moves === 1 ? "Next move here" : `${moves} next moves here`}
                  </span>
                ) : null}
                <span className="w-36 text-right text-xs text-muted tabular-nums">{groupAreas.length ? groupLine(counts) : "No areas yet"}</span>
                <span className="w-24">{groupAreas.length ? <StatusMixBar counts={counts} className="h-1.5" /> : null}</span>
              </span>
            </summary>
            {groupAreas.length ? (
              <GroupRows areas={groupAreas} look={look} moveRank={moveRank} />
            ) : (
              <p className="border-t border-hair px-4 py-3 text-sm text-faint">The catalog has no areas in this group yet.</p>
            )}
          </details>
        );
      })}
    </div>
  );
}

/** What the next steps are, said plainly: mostly shared data chores today, and nothing starts them on its own. */
export function nextStepsSentence(app: GrowthApp, apps: readonly GrowthApp[]): string {
  if (!app.nextMoves.length) return "No step is open: every area that applies is measured.";
  const { shared, shown, appCount } = sharedMoves(app, apps);
  const lead =
    shared * 2 >= shown
      ? `These are mostly data chores: ${shared} of these ${shown} steps also come up for most of your ${appCount} apps. They close gaps in what is measured; they do not yet say what to do about price, the store listing or ads.`
      : "In this order: out-of-date data first, then the gaps that unblock the most other areas.";
  return `${lead} Nothing starts them on its own: a step becomes work only when work is opened for it.`;
}
