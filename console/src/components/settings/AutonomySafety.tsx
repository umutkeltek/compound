import { ShieldIcon, StatusChip } from "@/components/kit";
import { arr, num, str, type Json } from "@/lib/compound";
import { plainAutonomy } from "@/lib/plain-words";

import { summarizeGrants, type GrantRow } from "./grants";
import { Card, CountChip, DoctrineNote, FileOwnedFoot, ForAgents, KV, SettingsSection } from "./primitives";

/**
 * Safety and permissions, in plain words: whether agents are stopped, how
 * far they may go on their own, which files they may edit, and the standing
 * permissions in force. Each card says what it controls, its value now and
 * who changes it; raw keys and commands sit behind a closed "For agents"
 * disclosure. Nothing here changes anything.
 *
 * Stopping every agent: `compound policy autonomy-halt --global` exists and
 * is checked before Compound starts or applies governed work (automatic
 * admission, governed runs, applied actions). It does not end an agent
 * session already open in a terminal, and the console has no way to set it.
 * The card says exactly that, once, with no button and no ritual.
 */

export const STOP_INSPECT_CMD = "compound policy autonomy-halt --json";
export const STOP_ALL_CMD = 'compound policy autonomy-halt --global --reason "stopped by the operator"';
export const STOP_CLEAR_CMD = "compound policy autonomy-halt --clear-global";

function GrantList({ rows }: { rows: GrantRow[] }) {
  return (
    <ul className="flex flex-col divide-y divide-hair">
      {rows.map((row) => (
        <li key={row.id} className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-0.5 py-2">
          <div className="min-w-0">
            <span className="text-sm text-ink">{row.what}</span>
            {row.irreversible ? <span className="ml-2 text-2xs font-semibold text-seam">cannot be undone</span> : null}
            {row.reason ? <p className="text-2xs text-muted">{row.reason}</p> : null}
            <p className="mono truncate text-2xs text-faint" title={row.target || row.capability}>
              {row.capability}{row.target ? ` · ${row.target}` : ""}
            </p>
          </div>
          <span className={`shrink-0 whitespace-nowrap text-2xs ${row.neverExpires ? "font-semibold text-ink" : "text-muted"}`}>{row.when}</span>
        </li>
      ))}
    </ul>
  );
}

export function AutonomySafety({
  halt,
  authority,
  grants,
  editable,
  generatedAt,
  nowMs,
}: {
  halt: Json | null;
  authority: Json | null;
  grants: Json | null;
  editable: Json | null;
  generatedAt?: string;
  /** the clock for which permissions are in force */
  nowMs: number;
}) {
  const globalHalt = Boolean(halt?.global_halt);
  const stoppedKinds = arr(halt?.disabled_classes).map((entry: Json) => str(entry?.class)).filter(Boolean);
  const stoppedCount = num(halt?.disabled_class_count) || stoppedKinds.length;
  const level = str(authority?.autonomy_level);
  const mode = str(authority?.mode);
  const summary = grants ? summarizeGrants(arr(grants.grants), nowMs) : null;
  const nothingEditable = Boolean(editable?.nothing_editable);
  const hardDeny = arr(editable?.hard_deny_core).map(String);
  const entryCount = num(editable?.entry_count);

  return (
    <SettingsSection
      index={1}
      icon={<ShieldIcon />}
      tone="seam"
      title="Safety and permissions"
      question={summary ? summary.sentence : "What agents may do without asking is not read yet."}
      source="compound policy standing-grants-authority --json"
      generatedAt={generatedAt}
    >
      <Card
        seamTone={halt === null ? undefined : globalHalt ? "crit" : stoppedCount > 0 ? "seam" : "good"}
        title="Stop all agents"
        badge={halt === null ? <StatusChip tone="muted" label="not read" /> : globalHalt ? <StatusChip tone="crit" label="stopped" /> : stoppedCount > 0 ? <StatusChip tone="seam" label={`${stoppedCount} kinds stopped`} /> : <StatusChip tone="good" label="not stopped" />}
      >
        <div className="flex flex-col gap-2 text-sm leading-relaxed text-muted">
          <p className="text-ink">
            {halt === null
              ? "Whether agents are stopped could not be read."
              : globalHalt
                ? `All agents are stopped${str(halt?.reason) ? `: ${str(halt?.reason)}` : "."}`
                : stoppedCount > 0
                  ? `Agents are stopped for ${stoppedCount === 1 ? "one kind of work" : `${stoppedCount} kinds of work`}${stoppedKinds.length ? ` (${stoppedKinds.join(", ")})` : ""}; everything else runs.`
                  : "Agents are not stopped."}
          </p>
          <p>
            Stopping them from here is not built yet. The stop that exists keeps Compound from starting work or carrying
            out actions on its own; agent sessions already open keep running until they are closed.
          </p>
        </div>
        <ForAgents cmds={[STOP_INSPECT_CMD, STOP_ALL_CMD, STOP_CLEAR_CMD]} />
      </Card>

      <Card
        title="Standing permissions"
        badge={summary ? <CountChip n={summary.active.length} label="in force" tone={summary.active.length > 0 ? "seam" : "good"} /> : <StatusChip tone="muted" label="not read" />}
      >
        {summary ? (
          <>
            {summary.active.length ? <GrantList rows={summary.active} /> : null}
            {summary.upcoming.length ? (
              <details className="mt-3 text-sm">
                <summary className="cursor-pointer text-xs font-semibold text-muted">{summary.upcoming.length.toLocaleString("en-US")} start later</summary>
                <GrantList rows={summary.upcoming} />
              </details>
            ) : null}
            {summary.expired.length ? (
              <details className="mt-3 text-sm">
                <summary className="cursor-pointer text-xs font-semibold text-muted">{summary.expired.length.toLocaleString("en-US")} expired, no longer in force</summary>
                <GrantList rows={summary.expired} />
              </details>
            ) : null}
          </>
        ) : (
          <DoctrineNote>The permissions could not be read, so this page does not say what agents may do.</DoctrineNote>
        )}
        <FileOwnedFoot
          cmd="compound policy standing-grants-authority --json"
          note="Permissions change only when their settings file is imported again; nothing in this console grants or removes one."
        />
      </Card>

      <div className="grid gap-4 md:grid-cols-2">
        <Card title="How far agents go on their own" badge={level ? <StatusChip tone="muted" label={level} /> : <StatusChip tone="muted" label="not read" />}>
          <p className="text-sm leading-relaxed text-ink">
            {authority === null ? "The autonomy level could not be read." : plainAutonomy(level) || (level ? `Level ${level}.` : "No autonomy level is set.")}
          </p>
          <FileOwnedFoot
            cmd="compound policy operator-authority --json"
            note="Set in your operator settings file; nothing in this console changes it."
          />
          <ForAgents label="For agents: technical record">
            <KV k="autonomy_level" v={level || "—"} />
            <KV k="mode" v={mode || "—"} />
            <KV k="installed" v={authority?.installed ? "yes" : "no"} />
          </ForAgents>
        </Card>

        <Card title="Changes agents may make to their own setup" badge={editable === null ? <StatusChip tone="muted" label="not read" /> : <CountChip n={entryCount} label="open" tone={nothingEditable ? "good" : "muted"} />}>
          <p className="text-sm leading-relaxed text-ink">
            {editable === null
              ? "Which parts of their own setup agents may change could not be read."
              : nothingEditable
                ? "None: agents may not propose changes to their own setup until a part is opened for it."
                : `${entryCount.toLocaleString("en-US")} ${entryCount === 1 ? "part of their setup is" : "parts of their setup are"} open for agents to propose changes.`}{" "}
            {hardDeny.length ? `The ${hardDeny.length.toLocaleString("en-US")} parts that guard them (rules, permissions, records, grading) can never be opened.` : ""}
          </p>
          <FileOwnedFoot cmd="compound policy editable-surfaces --json" note="The list is a settings file; nothing in this console changes it." />
          <ForAgents label="For agents: technical record">
            <KV k="installed" v={editable?.installed ? "yes" : "no"} />
            <KV k="nothing_editable" v={nothingEditable ? "true" : "false"} />
            <KV k="hard_deny_core" v={hardDeny.length ? hardDeny.join(", ") : "—"} mono={false} />
          </ForAgents>
        </Card>
      </div>
    </SettingsSection>
  );
}
