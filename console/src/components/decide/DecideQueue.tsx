"use client";

import * as Dialog from "@radix-ui/react-dialog";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";

import { CopyCmd } from "@/components/CopyCmd";
import { Button, FreshnessChip } from "@/components/kit";
import { remainingDecideMembers } from "@/lib/decide-members";
import { genericReviewResolutionActs } from "@/lib/decide-resolution-acts";
import type { DecideBrief, DecideBriefOption, DecideDomain, DecideGroup, DecideMember } from "@/lib/decide-model";
import { decideActIdentity } from "@/lib/governed-action-client";
import { GovernedAct, type ExecuteResult } from "./GovernedAct";

/* Domain hue is accent-only (glyph tile + dot); the needs-you seam is amber. */
const DOMAIN_TILE: Record<DecideDomain, string> = {
  work: "border-work-line bg-work-soft text-work",
  memory: "border-memory-line bg-memory-soft text-memory",
  capability: "border-capability-line bg-capability-soft text-capability",
  route: "border-route-line bg-route-soft text-route",
  product: "border-product-line bg-product-soft text-product",
  neutral: "border-hair bg-soft-2 text-muted",
};

function operatorText(value: string): string {
  return value
    .replace(/\bclaude_code\b/gi, "Claude Code")
    .replace(/\bswitchback_api\b/gi, "Switchback API");
}

function actorLabel(value: string): string {
  return value
    .split(/[_-]+/)
    .filter(Boolean)
    .map((part) => {
      const lower = part.toLowerCase();
      if (lower === "api") return "API";
      if (lower === "codex") return "Codex";
      if (lower === "claude") return "Claude";
      if (lower === "switchback") return "Switchback";
      return part.charAt(0).toUpperCase() + part.slice(1);
    })
    .join(" ");
}

function GlyphTile({ domain, glyph }: { domain: DecideDomain; glyph: string }) {
  return (
    <span
      aria-hidden
      className={`inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-sm border text-xs font-bold ${DOMAIN_TILE[domain]}`}
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

/* ---------- decision-brief translation (propose-only; display-only) ----------
 * Renders ONLY when a card's group carries a prepared, already-validated
 * `brief` (see decide-model.ts groupBrief — scans every group member, not
 * just group.sample). Un-briefed cards render with `group.brief` undefined/
 * null, so this whole block is absent and every byte above/below it is
 * exactly what rendered before this packet (the frozen-markup guarantee).
 * `command_argv` is copy/display material ONLY — never wired to a fetch, so
 * a brief can never introduce a new execution path (see AGENTS.md boundary:
 * "Do not wire brief-sourced argv into /api/decision/execute").
 */
function BriefOptionRow({ option, recommended }: { option: DecideBriefOption; recommended: boolean }) {
  const argv = option.commandArgv;
  const copyCmd = argv && argv.length ? (argv[0] === "compound" ? argv.join(" ") : `compound ${argv.join(" ")}`) : "";
  return (
    <div
      className={`rounded-md border px-3 py-2 text-xs leading-relaxed ${
        recommended ? "border-good/40 bg-good/5" : "border-hair bg-soft"
      }`}
    >
      <div className="flex items-center gap-1.5">
        <span className="font-semibold text-ink">{option.label || option.value}</span>
        {recommended ? (
          <span className="rounded-pill bg-good/15 px-1.5 py-0.5 text-2xs font-semibold text-good">recommended</span>
        ) : null}
      </div>
      {option.consequenceApprove ? (
        <div className="mt-1 text-ink">
          <span className="font-semibold text-good">if chosen</span>
          <span className="text-faint"> → </span>
          {option.consequenceApprove}
        </div>
      ) : null}
      {option.consequenceHold ? (
        <div className="mt-0.5 text-muted">
          <span className="font-semibold">if not</span>
          <span> → </span>
          {option.consequenceHold}
        </div>
      ) : null}
      {copyCmd ? (
        <div className="mt-1.5">
          <CopyCmd cmd={copyCmd} compact />
        </div>
      ) : null}
    </div>
  );
}

function BriefPanel({ brief }: { brief: DecideBrief }) {
  return (
    <div className="mt-3 rounded-md border border-hair bg-page px-3 py-2.5">
      <div className="flex items-start justify-between gap-2">
        <div className="text-sm font-semibold leading-snug text-ink">{brief.headline}</div>
        <span
          className="shrink-0 rounded-pill border border-hair bg-soft px-2 py-0.5 text-2xs font-medium text-muted"
          title="This translation was prepared by an agent session for you — not the operator, not auto-applied."
        >
          prepared by {actorLabel(brief.preparedBy)}
        </span>
      </div>
      {brief.whatThisIs ? <p className="mt-1 text-xs leading-relaxed text-muted">{brief.whatThisIs}</p> : null}
      {brief.findings.length ? (
        <ul className="mt-2 flex flex-col gap-1">
          {brief.findings.map((f, i) => (
            <li key={i} className="text-xs leading-relaxed text-ink">
              <span>{f.finding}</span>{" "}
              {f.evidenceRef ? (
                <code className="mono text-2xs text-faint" title={f.evidenceRef}>
                  {f.evidenceRef}
                </code>
              ) : null}
            </li>
          ))}
        </ul>
      ) : null}
      {brief.options.length ? (
        <div className="mt-2 flex flex-col gap-1.5">
          {brief.options.map((o, i) => (
            <BriefOptionRow
              key={o.value || i}
              option={o}
              recommended={Boolean(brief.recommendation.option) && o.value === brief.recommendation.option}
            />
          ))}
        </div>
      ) : null}
      {brief.recommendation.rationale ? (
        <p className="mt-2 text-2xs leading-relaxed text-faint">
          <span className="font-semibold text-muted">why this lean</span> — {brief.recommendation.rationale}
          {brief.recommendation.confidence ? ` (confidence ${Math.round(brief.recommendation.confidence * 100)}%)` : ""}
        </p>
      ) : null}
    </div>
  );
}

/* ---------- enumerable batch: per-member checkboxes + exclusion ---------- */

function EnumerableApply({
  area,
  members,
  excluded,
  onToggle,
  mutationsEnabled,
  requiresConfirmation,
  requiresRationale,
  policyNote,
  label,
}: {
  area: string;
  members: DecideMember[];
  excluded: Set<string>;
  onToggle: (id: string) => void;
  mutationsEnabled: boolean;
  requiresConfirmation: boolean;
  requiresRationale: boolean;
  policyNote: string;
  label: string;
}) {
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const [running, setRunning] = useState(false);
  const [progress, setProgress] = useState<{ done: number; total: number } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [rationale, setRationale] = useState("");
  const [completed, setCompleted] = useState<Set<string>>(() => new Set());

  const included = remainingDecideMembers(members, excluded, completed);

  const apply = useCallback(async (operatorRationale: string) => {
    if (!mutationsEnabled || running || included.length === 0) return;
    if (requiresRationale && !operatorRationale.trim()) {
      setError("Rationale is required.");
      return;
    }
    setRunning(true);
    setError(null);
    setProgress({ done: 0, total: included.length });
    let failed = "";
    let completedThisRun = 0;
    for (let i = 0; i < included.length; i++) {
      const member = included[i];
      try {
        const response = await fetch("/api/decision/execute", {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify({
            area,
            itemId: member.id,
            actionId: member.actionId,
            expectedRevision: member.expectedRevision,
            confirmed: requiresConfirmation,
            rationale: operatorRationale.trim(),
          }),
        });
        const body = (await response.json().catch(() => null)) as { ok?: boolean; stderr?: string } | null;
        if (!response.ok || !body?.ok) {
          failed = body?.stderr || `member ${member.id} failed (${response.status})`;
          break;
        }
      } catch (e) {
        failed = e instanceof Error ? e.message : "request failed";
        break;
      }
      completedThisRun = i + 1;
      setCompleted((previous) => new Set(previous).add(member.id));
      setProgress({ done: completedThisRun, total: included.length });
    }
    setRunning(false);
    if (failed) {
      setProgress({ done: completedThisRun, total: included.length });
      setError(completedThisRun > 0 ? `Applied ${completedThisRun} of ${included.length}; ${failed}. Refreshing owner truth.` : failed);
      router.refresh();
    }
    else {
      setOpen(false);
      router.refresh();
    }
  }, [area, included, mutationsEnabled, requiresConfirmation, requiresRationale, running, router]);

  const trigger = useCallback(() => {
    if (!mutationsEnabled || running || included.length === 0) return;
    setError(null);
    if (requiresConfirmation) {
      setOpen(true);
      return;
    }
    void apply("");
  }, [apply, included.length, mutationsEnabled, requiresConfirmation, running]);

  return (
    <div className="flex flex-col gap-2">
      <ul className="flex flex-col gap-1">
        {members.map((m) => {
          const isCompleted = completed.has(m.id);
          const isIncluded = !excluded.has(m.id) && !isCompleted;
          return (
            <li key={m.id}>
              <label className={`flex min-h-8 cursor-pointer items-center gap-2 rounded-sm px-2 py-1 ${isCompleted ? "cursor-default opacity-60" : "hover:bg-soft"}`}>
                <input
                  type="checkbox"
                  checked={isIncluded}
                  disabled={isCompleted}
                  onChange={() => onToggle(m.id)}
                  className="h-4 w-4 shrink-0 accent-work"
                />
                <span className="min-w-0 flex-1 truncate text-xs text-muted" title={m.id}>
                  {m.label}{isCompleted ? " · applied" : ""}
                </span>
                <code className="mono shrink-0 text-2xs text-faint" title={m.id}>
                  {m.id.length > 18 ? `${m.id.slice(0, 16)}…` : m.id}
                </code>
              </label>
            </li>
          );
        })}
      </ul>
      <div className="flex flex-wrap items-center gap-2">
        <Button
          variant="primary"
          disabled={!mutationsEnabled || running || included.length === 0}
          onClick={trigger}
          title={mutationsEnabled ? undefined : "Set COMPOUND_CONSOLE_MUTATIONS=1 to act."}
        >
          {running
            ? `Applying ${progress?.done ?? 0}/${progress?.total ?? included.length}…`
            : requiresConfirmation
              ? `Review ${included.length} of ${members.length}`
              : `Apply ${included.length} of ${members.length}`}
        </Button>
        <span className="text-2xs text-faint">each member is applied on its own; excluded members are skipped</span>
      </div>
      {!open && error ? <span role="alert" className="text-2xs leading-snug text-crit">{error}</span> : null}

      <Dialog.Root open={open} onOpenChange={(next) => { if (!running) setOpen(next); }}>
        <Dialog.Portal>
          <Dialog.Overlay className="fixed inset-0 z-50 bg-strong/35 backdrop-blur-sm motion-reduce:backdrop-blur-none" />
          <Dialog.Content className="fixed top-1/2 left-1/2 z-50 max-h-[calc(100vh-2rem)] w-[calc(100vw-2rem)] max-w-md -translate-x-1/2 -translate-y-1/2 overflow-y-auto overscroll-contain rounded-lg border border-hair bg-panel p-5 shadow-lg focus:outline-none">
            <Dialog.Title className="text-md font-semibold text-ink">Confirm {label}</Dialog.Title>
            <Dialog.Description className="mt-2 text-sm leading-relaxed text-muted">
              Applies {included.length} selected member{included.length === 1 ? "" : "s"} one at a time. Excluded members remain unchanged; completed members are not retried.
            </Dialog.Description>
            {policyNote ? <div className="mt-3 rounded-md border border-hair bg-soft px-3 py-2 text-xs leading-relaxed text-muted">{policyNote}</div> : null}
            {requiresRationale ? (
              <label className="mt-3 flex flex-col gap-1">
                <span className="text-2xs font-bold uppercase text-faint">Rationale <span className="text-crit">*</span></span>
                <textarea
                  value={rationale}
                  onChange={(event) => { setRationale(event.target.value); setError(null); }}
                  required
                  placeholder="Why is this the right reviewed action? This becomes decision provenance."
                  className="min-h-24 resize-y rounded-md border border-hair bg-panel px-3 py-2 text-sm text-ink outline-none focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-focus"
                />
              </label>
            ) : null}
            {error ? (
              <div role="alert" aria-live="assertive" className="mt-3 rounded-md border border-crit-line bg-crit-soft px-3 py-2 text-xs leading-relaxed text-crit">
                {error}
              </div>
            ) : null}
            <div className="mt-4 flex justify-end gap-2">
              <Dialog.Close asChild><Button variant="secondary" disabled={running}>Cancel</Button></Dialog.Close>
              <Button
                variant="primary"
                disabled={running || (requiresRationale && !rationale.trim())}
                onClick={() => void apply(rationale)}
              >
                {running ? `Applying ${progress?.done ?? 0}/${progress?.total ?? included.length}…` : `Confirm ${included.length}`}
              </Button>
            </div>
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
    </div>
  );
}

/* ---------- read-only member list (atomic-run / bespoke / copy) ---------- */

function ReadonlyMembers({ members, atomic }: { members: DecideMember[]; atomic: boolean }) {
  if (members.length <= 1) return null;
  return (
    <details className="mt-3 rounded-md border border-hair bg-soft px-3 py-2 text-xs text-muted">
      <summary className="cursor-pointer font-medium text-muted">Members · {members.length}</summary>
      {atomic ? (
        <p className="mt-2 text-2xs leading-relaxed text-faint">
          This act is run-scoped — it applies to every member together; members cannot be excluded one by one.
        </p>
      ) : null}
      <ul className="mt-2 flex flex-col gap-1">
        {members.slice(0, 60).map((m) => (
          <li key={m.id} className="flex items-center justify-between gap-2">
            <span className="min-w-0 truncate text-2xs text-muted" title={m.id}>
              {m.label}
            </span>
            <code className="mono shrink-0 text-2xs text-faint" title={m.id}>
              {m.id.length > 16 ? `${m.id.slice(0, 14)}…` : m.id}
            </code>
          </li>
        ))}
      </ul>
      {members.length > 60 ? (
        <p className="mt-1 text-2xs text-faint">+{members.length - 60} more not shown.</p>
      ) : null}
    </details>
  );
}

const CAPABILITY_TONE: Record<DecideGroup["capability"]["kind"], string> = {
  apply: "border-good-line bg-good-soft text-good",
  record: "border-hair bg-soft text-muted",
  track: "border-work-line bg-work-soft text-work",
  unavailable: "border-seam-line bg-seam-soft text-seam",
};

function CapabilityStatus({ group, compact = false }: { group: DecideGroup; compact?: boolean }) {
  return (
    <span
      className={`inline-flex shrink-0 items-center rounded-pill border font-semibold ${CAPABILITY_TONE[group.capability.kind]} ${
        compact ? "px-2 py-0.5 text-2xs" : "px-2.5 py-1 text-xs"
      }`}
      title={group.capability.detail}
    >
      {group.capability.label}
    </span>
  );
}

function DecisionRow({
  group,
  selected,
  rowRef,
  onActivate,
  onFocus,
  onKeyDown,
}: {
  group: DecideGroup;
  selected: boolean;
  rowRef: (element: HTMLButtonElement | null) => void;
  onActivate: () => void;
  onFocus: () => void;
  onKeyDown: (event: React.KeyboardEvent<HTMLButtonElement>) => void;
}) {
  return (
    <button
      ref={rowRef}
      type="button"
      aria-pressed={selected}
      onClick={onActivate}
      onFocus={onFocus}
      onKeyDown={onKeyDown}
      className={`grid w-full grid-cols-[auto_1fr] gap-3 border-b border-l-2 border-b-hair px-3 py-3 text-left outline-none transition-colors last:border-b-0 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-focus ${
        selected ? "border-l-seam bg-soft" : "border-l-transparent bg-panel hover:bg-soft"
      }`}
    >
      <GlyphTile domain={group.domain} glyph={group.glyph} />
      <span className="min-w-0">
        <span className="flex items-center justify-between gap-2">
          <span className="truncate text-2xs font-bold uppercase text-faint" title={group.areaPlain}>{group.areaLabel}</span>
          <span className={`shrink-0 text-2xs tabular-nums ${group.old ? "font-semibold text-seam" : "text-faint"}`}>{group.ageLabel}</span>
        </span>
        <span className="mt-1 line-clamp-2 block text-sm font-semibold leading-snug text-ink">{operatorText(group.title)}</span>
        <span className="mt-2 flex flex-wrap items-center gap-2">
          <CapabilityStatus group={group} compact />
          {group.count > 1 ? <span className="mono text-2xs text-faint">{group.count} items</span> : null}
        </span>
      </span>
    </button>
  );
}

function EvidenceSection({ group }: { group: DecideGroup }) {
  const rows = (
    <div className="divide-y divide-hair border-y border-hair">
      {group.evidence.map((evidence, index) => (
        <div key={`${evidence.ref}:${index}`} className="grid gap-1 py-2.5">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <span className="text-xs font-semibold text-ink">{evidence.label}</span>
            {evidence.sourceKind ? <span className="rounded-pill border border-hair bg-soft px-2 py-0.5 text-2xs text-faint">{evidence.sourceKind}</span> : null}
          </div>
          {evidence.summary ? <p className="text-xs leading-relaxed text-muted">{evidence.summary}</p> : null}
          {evidence.ref ? <code className="mono break-all text-2xs text-faint">{evidence.ref}</code> : null}
          {evidence.openCommand ? <div><CopyCmd cmd={evidence.openCommand} compact /></div> : null}
        </div>
      ))}
    </div>
  );

  return (
    <details className="group border-t border-hair px-4 py-4">
      <summary className="flex cursor-pointer list-none items-center justify-between gap-3 text-xs font-semibold text-muted marker:hidden">
        <span>Evidence and record details</span>
        <span className="inline-flex items-center gap-2">
          {group.evidence.length ? <span className="mono text-2xs font-normal text-faint">{group.evidence.length} sources</span> : null}
          <span aria-hidden className="text-sm text-faint transition-transform group-open:rotate-90">›</span>
        </span>
      </summary>
      <div className="mt-3 flex flex-wrap items-center gap-3 rounded-md bg-soft px-3 py-2">
        <code className="mono text-2xs text-faint" title={group.sampleId}>{group.sampleId}</code>
        <span className={`text-2xs tabular-nums ${group.old ? "font-semibold text-seam" : "text-faint"}`}>
          {group.ageLabel}{group.old ? " · stale" : ""}
        </span>
        <FreshnessChip generatedAt={group.freshness.generatedAt} source={group.freshness.source} />
      </div>
      {group.evidence.length ? <div className="mt-3">{rows}</div> : null}
    </details>
  );
}

function decisionIdentity(group: DecideGroup): string {
  const { act } = group;
  if (act.mode === "bespoke") {
    return `${group.key}:${act.mode}:${act.itemId}:${act.expectedRevision}:${act.options.map((option) => option.value).join(",")}`;
  }
  if (act.mode === "generic") return `${group.key}:${act.mode}:${act.itemId}:${act.actionId}:${act.expectedRevision}:${act.requiresConfirmation}:${act.effectKind}`;
  if (act.mode === "batch-atomic") return `${group.key}:${act.mode}:${act.itemId}:${act.option}:${act.argv?.join(" ") ?? ""}`;
  if (act.mode === "batch-enumerable") {
    return `${group.key}:${act.mode}:${act.requiresConfirmation}:${act.requiresRationale}:${act.effectKind}:${act.members.map((member) => `${member.id}:${member.actionId}:${member.expectedRevision}`).join("|")}`;
  }
  return `${group.key}:${act.mode}:${act.command}`;
}

function DecisionDetail({
  group,
  mutationsEnabled,
  excluded,
  onToggleMember,
  retainedOutcome,
  onExecuted,
}: {
  group: DecideGroup;
  mutationsEnabled: boolean;
  excluded: Set<string>;
  onToggleMember: (id: string) => void;
  retainedOutcome?: ExecuteResult | null;
  onExecuted: (result: ExecuteResult) => void;
}) {
  const { act } = group;
  const effectKind = group.capability.kind === "unavailable" ? "apply" : group.capability.kind;
  const reviewResolutionActs = genericReviewResolutionActs(group);
  return (
    <article className="overflow-hidden rounded-lg border border-hair bg-panel shadow-sm">
      <header className="px-4 py-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="flex min-w-0 items-start gap-3">
            <GlyphTile domain={group.domain} glyph={group.glyph} />
            <div className="min-w-0">
              <div className="text-2xs font-bold uppercase text-faint" title={group.areaPlain}>{group.areaLabel}</div>
              <h3 className="mt-1 text-lg font-semibold leading-snug text-ink">{operatorText(group.title)}</h3>
            </div>
          </div>
          <CapabilityStatus group={group} />
        </div>
      </header>

      <section className="border-t border-hair px-4 py-4">
        {group.body ? <p className="max-w-read text-sm leading-relaxed text-muted">{group.body}</p> : null}
        {group.consequences && !(act.mode === "bespoke" && act.options.length > 1) ? (
          <ConsequencePair approve={group.consequences.approve} hold={group.consequences.hold} />
        ) : null}
      </section>

      <section className="border-t border-hair bg-soft/60 px-4 py-4">
        <div>
          {act.mode === "copy" ? (
            <div className="grid gap-2 border-l-2 border-seam-line pl-3">
              <p className="text-sm font-semibold text-seam">Unavailable: owner workflow missing</p>
              <p className="text-xs leading-relaxed text-muted">{act.reason || "This review remains inspectable, but the owner has not exposed a safe state-changing action."}</p>
              <div className="flex flex-wrap items-center gap-2">
                <CopyCmd cmd={act.command} compact />
                {reviewResolutionActs.map((resolutionAct) => (
                  <GovernedAct
                    key={resolutionAct.actionId}
                    act={resolutionAct}
                    mutationsEnabled={mutationsEnabled}
                    effectKind="record"
                  />
                ))}
              </div>
            </div>
          ) : act.mode === "batch-enumerable" ? (
            <EnumerableApply
              area={act.area}
              members={act.members}
              excluded={excluded}
              onToggle={onToggleMember}
              mutationsEnabled={mutationsEnabled}
              requiresConfirmation={act.requiresConfirmation}
              requiresRationale={act.requiresRationale}
              policyNote={act.policyNote}
              label={act.label}
            />
          ) : (
            <GovernedAct
              key={decideActIdentity(act)}
              act={act}
              mutationsEnabled={mutationsEnabled}
              onExecuted={onExecuted}
              retainedOutcome={retainedOutcome}
              effectKind={effectKind}
            />
          )}
        </div>
      </section>

      {group.brief ? (
        <details className="group border-t border-hair px-4 py-4">
          <summary className="flex cursor-pointer list-none items-center justify-between gap-3 text-xs font-semibold text-muted marker:hidden">
            <span>Prepared analysis</span>
            <span aria-hidden className="text-sm text-faint transition-transform group-open:rotate-90">›</span>
          </summary>
          <div className="mt-3"><BriefPanel brief={group.brief} /></div>
        </details>
      ) : null}

      <EvidenceSection group={group} />

      {group.members.length > 1 ? (
        <section className="border-t border-hair px-4 py-4">
          <ReadonlyMembers members={group.members} atomic={act.mode === "batch-atomic"} />
        </section>
      ) : null}
    </article>
  );
}

type RetainedDecision = { group: DecideGroup; result: ExecuteResult; identity: string };

export function DecideQueue({ groups, mutationsEnabled }: { groups: DecideGroup[]; mutationsEnabled: boolean }) {
  const [filter, setFilter] = useState("");
  const [focused, setFocused] = useState(0);
  const [selectedKey, setSelectedKey] = useState(groups[0]?.key ?? "");
  const [excluded, setExcluded] = useState<Record<string, Set<string>>>({});
  const [retained, setRetained] = useState<Record<string, RetainedDecision>>({});

  const filterRef = useRef<HTMLInputElement | null>(null);
  const rowRefs = useRef<(HTMLButtonElement | null)[]>([]);
  const listRef = useRef<HTMLDivElement | null>(null);
  const detailRef = useRef<HTMLDivElement | null>(null);

  const visible = useMemo(() => {
    const query = filter.trim().toLowerCase();
    if (!query) return groups;
    return groups.filter((group) => `${group.title} ${group.areaLabel} ${group.body}`.toLowerCase().includes(query));
  }, [groups, filter]);

  useEffect(() => {
    const selectedIsCurrent = groups.some((group) => group.key === selectedKey);
    const selectedIsVisible = visible.some((group) => group.key === selectedKey);
    if (!selectedKey && visible[0]) {
      setSelectedKey(visible[0].key);
    } else if (selectedIsCurrent && !selectedIsVisible) {
      setSelectedKey(visible[0]?.key ?? "");
    } else if (selectedKey && !selectedIsCurrent && !retained[selectedKey]) {
      setSelectedKey(visible[0]?.key ?? "");
    }
    if (focused > visible.length - 1) setFocused(Math.max(0, visible.length - 1));
  }, [focused, groups, retained, selectedKey, visible]);

  useEffect(() => {
    function onKey(event: KeyboardEvent) {
      const element = event.target as HTMLElement | null;
      const typing = Boolean(element?.closest("input, textarea, select, [contenteditable=true]"));
      if (event.key === "/" && !typing) {
        event.preventDefault();
        filterRef.current?.focus();
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const focusRow = useCallback((index: number) => {
    const clamped = Math.max(0, Math.min(index, rowRefs.current.length - 1));
    setFocused(clamped);
    rowRefs.current[clamped]?.focus();
  }, []);

  const select = useCallback((group: DecideGroup, index: number, scroll = false) => {
    setSelectedKey(group.key);
    setFocused(index);
    if (scroll && window.matchMedia("(max-width: 1023px)").matches) {
      window.setTimeout(() => detailRef.current?.scrollIntoView({ behavior: "smooth", block: "start" }), 0);
    }
  }, []);

  function onRowKey(event: React.KeyboardEvent<HTMLButtonElement>, index: number, group: DecideGroup) {
    if (event.key === "j" || event.key === "ArrowDown") {
      event.preventDefault();
      focusRow(index + 1);
    } else if (event.key === "k" || event.key === "ArrowUp") {
      event.preventDefault();
      focusRow(index - 1);
    } else if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      select(group, index, true);
    }
  }

  const selected = visible.find((group) => group.key === selectedKey) ?? retained[selectedKey]?.group ?? visible[0] ?? null;
  const liveMessage = selected ? `Selected decision: ${operatorText(selected.title)}` : "Nothing waits on you.";
  const selectedIdentity = selected ? decisionIdentity(selected) : "";
  const selectedRetained = selected ? retained[selected.key] : undefined;

  return (
    <div className="grid gap-3">
      <div aria-live="polite" className="sr-only">{liveMessage}</div>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <input
          ref={filterRef}
          value={filter}
          onChange={(event) => setFilter(event.target.value)}
          placeholder="Filter decisions"
          aria-label="Filter decisions"
          className="w-full rounded-md border border-hair bg-panel px-3 py-2 text-sm text-ink outline-none focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-focus sm:max-w-xs"
        />
        <span className="mono text-2xs tabular-nums text-faint">{visible.length} decisions</span>
      </div>

      <div className="grid items-start gap-4 lg:grid-cols-5">
        <div ref={listRef} className="scroll-mt-16 max-h-[52vh] overflow-y-auto rounded-lg border border-hair bg-panel lg:col-span-2 lg:max-h-none lg:overflow-visible">
          {visible.length ? visible.map((group, index) => (
            <DecisionRow
              key={group.key}
              group={group}
              selected={selected?.key === group.key}
              rowRef={(element) => { rowRefs.current[index] = element; }}
              onActivate={() => select(group, index, true)}
              onFocus={() => select(group, index)}
              onKeyDown={(event) => onRowKey(event, index, group)}
            />
          )) : (
            <p className="border-dashed px-4 py-8 text-center text-sm text-muted">No decision matches “{filter}”.</p>
          )}
        </div>

        <div ref={detailRef} className="scroll-mt-20 lg:sticky lg:top-20 lg:col-span-3">
          {selected ? (
            <div className="grid gap-2">
              <button
                type="button"
                onClick={() => listRef.current?.scrollIntoView({ behavior: "smooth", block: "start" })}
                className="inline-flex w-fit items-center gap-1.5 rounded-md border border-hair bg-panel px-2.5 py-1.5 text-xs font-semibold text-muted hover:bg-soft lg:hidden"
              >
                <span aria-hidden>↑</span>
                All decisions
              </button>
              <DecisionDetail
                key={selectedIdentity}
                group={selected}
                mutationsEnabled={mutationsEnabled}
                excluded={excluded[selected.key] ?? new Set()}
                onToggleMember={(id) => setExcluded((previous) => {
                  const next = new Set(previous[selected.key] ?? []);
                  if (next.has(id)) next.delete(id); else next.add(id);
                  return { ...previous, [selected.key]: next };
                })}
                retainedOutcome={selectedRetained?.identity === selectedIdentity ? selectedRetained.result : null}
                onExecuted={(result) => setRetained((previous) => ({
                  ...previous,
                  [selected.key]: { group: selected, result, identity: selectedIdentity },
                }))}
              />
            </div>
          ) : (
            <div className="rounded-lg border border-dashed border-hair bg-page px-4 py-10 text-center text-sm text-muted">Clean seam. Nothing waits on you.</div>
          )}
        </div>
      </div>
    </div>
  );
}
