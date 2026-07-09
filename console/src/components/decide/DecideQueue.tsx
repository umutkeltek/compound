"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";

import { CopyCmd } from "@/components/CopyCmd";
import { Button, DocIcon, Drawer, FreshnessChip, MetaCount, UsersIcon } from "@/components/kit";
import type { DecideDomain, DecideGroup, DecideMember } from "@/lib/decide-model";
import { GovernedAct } from "./GovernedAct";

/* Domain hue is accent-only (glyph tile + dot); the needs-you seam is amber. */
const DOMAIN_TILE: Record<DecideDomain, string> = {
  work: "border-work-line bg-work-soft text-work",
  memory: "border-memory-line bg-memory-soft text-memory",
  capability: "border-capability-line bg-capability-soft text-capability",
  route: "border-route-line bg-route-soft text-route",
  product: "border-product-line bg-product-soft text-product",
  neutral: "border-hair bg-soft-2 text-muted",
};

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

/* ---------- enumerable batch: per-member checkboxes + exclusion ---------- */

function EnumerableApply({
  area,
  members,
  excluded,
  cursor,
  onToggle,
  mutationsEnabled,
  registerTrigger,
}: {
  area: string;
  members: DecideMember[];
  excluded: Set<string>;
  cursor: number;
  onToggle: (id: string) => void;
  mutationsEnabled: boolean;
  registerTrigger: (fn: (() => void) | null) => void;
}) {
  const router = useRouter();
  const [running, setRunning] = useState(false);
  const [progress, setProgress] = useState<{ done: number; total: number } | null>(null);
  const [error, setError] = useState<string | null>(null);

  const included = members.filter((m) => !excluded.has(m.id));

  const apply = useCallback(async () => {
    if (!mutationsEnabled || running || included.length === 0) return;
    setRunning(true);
    setError(null);
    setProgress({ done: 0, total: included.length });
    let failed = "";
    for (let i = 0; i < included.length; i++) {
      const member = included[i];
      try {
        const response = await fetch("/api/decision/execute", {
          method: "POST",
          headers: { "content-type": "application/json" },
          body: JSON.stringify({ area, itemId: member.id, actionId: member.actionId, confirmed: true }),
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
      setProgress({ done: i + 1, total: included.length });
    }
    setRunning(false);
    if (failed) setError(failed);
    else router.refresh();
  }, [area, included, mutationsEnabled, running, router]);

  useEffect(() => {
    registerTrigger(() => void apply());
    return () => registerTrigger(null);
  }, [apply, registerTrigger]);

  return (
    <div className="flex flex-col gap-2">
      <ul className="flex flex-col gap-1">
        {members.map((m, i) => {
          const isIncluded = !excluded.has(m.id);
          return (
            <li
              key={m.id}
              className={`flex items-center gap-2 rounded-sm px-2 py-1 ${i === cursor ? "outline outline-2 outline-offset-2 outline-work" : ""}`}
            >
              <input
                type="checkbox"
                checked={isIncluded}
                onChange={() => onToggle(m.id)}
                aria-label={`include ${m.label}`}
                className="accent-work"
              />
              <span className="min-w-0 flex-1 truncate text-xs text-muted" title={m.id}>
                {m.label}
              </span>
              <code className="mono shrink-0 text-2xs text-faint" title={m.id}>
                {m.id.length > 18 ? `${m.id.slice(0, 16)}…` : m.id}
              </code>
            </li>
          );
        })}
      </ul>
      <div className="flex flex-wrap items-center gap-2">
        <Button
          variant="primary"
          disabled={!mutationsEnabled || running || included.length === 0}
          onClick={() => void apply()}
          title={mutationsEnabled ? undefined : "Set COMPOUND_CONSOLE_MUTATIONS=1 to act."}
        >
          {running
            ? `Applying ${progress?.done ?? 0}/${progress?.total ?? included.length}…`
            : `Apply ${included.length} of ${members.length}`}
        </Button>
        <span className="text-2xs text-faint">each member is applied on its own; excluded members are skipped</span>
      </div>
      {error ? <span className="text-2xs leading-snug text-crit">{error}</span> : null}
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

/* ---------- one decision card ---------- */

function DecideCard({
  group,
  index,
  focused,
  evidenceOpen,
  excluded,
  memberCursor,
  mutationsEnabled,
  cardRef,
  onFocus,
  onKey,
  onOpenDetail,
  onToggleMember,
  registerTrigger,
}: {
  group: DecideGroup;
  index: number;
  focused: boolean;
  evidenceOpen: boolean;
  excluded: Set<string>;
  memberCursor: number;
  mutationsEnabled: boolean;
  cardRef: (el: HTMLElement | null) => void;
  onFocus: () => void;
  onKey: (e: React.KeyboardEvent) => void;
  onOpenDetail: () => void;
  onToggleMember: (id: string) => void;
  registerTrigger: (fn: (() => void) | null) => void;
}) {
  const { act } = group;
  return (
    <article
      ref={cardRef}
      tabIndex={0}
      data-decidecard
      aria-label={`${group.areaLabel}: ${group.title}`}
      onFocus={onFocus}
      onKeyDown={onKey}
      className={`admission-seam rounded-lg border border-hair bg-panel px-4 py-3.5 shadow-sm transition-shadow hover:shadow ${
        focused ? "outline outline-2 outline-offset-2 outline-work" : ""
      }`}
    >
      <div className="flex items-start justify-between gap-2">
        <div className="flex min-w-0 items-start gap-2">
          <GlyphTile domain={group.domain} glyph={group.glyph} />
          <div className="min-w-0">
            <div className="flex items-center gap-1.5">
              <span className="text-2xs font-bold uppercase tracking-wide text-faint" title={group.areaPlain}>
                {group.areaLabel}
              </span>
              {group.count > 1 ? (
                <span className="mono text-2xs tabular-nums text-faint">· {group.count}</span>
              ) : null}
            </div>
            <h3 className="mt-0.5 text-md font-semibold leading-snug text-ink">{group.title}</h3>
          </div>
        </div>
        <span
          className={`shrink-0 rounded-pill border border-seam-line bg-seam-soft px-2.5 py-0.5 text-xs font-semibold text-seam ${
            group.old ? "" : ""
          }`}
        >
          needs you
        </span>
      </div>

      {group.unlock ? <div className="mt-1 text-xs font-medium text-good">→ {group.unlock}</div> : null}
      {group.body ? (
        <p className="mt-2 text-sm leading-relaxed text-muted" style={{ maxWidth: "var(--read-max)" }}>
          {group.body}
        </p>
      ) : null}
      {group.consequences ? <ConsequencePair approve={group.consequences.approve} hold={group.consequences.hold} /> : null}

      <div className="mt-3 flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-2.5">
          <span className={`text-xs tabular-nums ${group.old ? "font-semibold text-seam" : "text-faint"}`}>
            {group.ageLabel}
            {group.old ? " · stale" : ""}
          </span>
          {group.members.length > 1 ? (
            <MetaCount icon={<UsersIcon />} count={group.members.length} title="members grouped into this decision" />
          ) : null}
          {group.evidence.length ? (
            <MetaCount icon={<DocIcon />} count={group.evidence.length} title="evidence refs" />
          ) : null}
          <FreshnessChip generatedAt={group.freshness.generatedAt} source={group.freshness.source} />
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="secondary" onClick={onOpenDetail}>
            Details
          </Button>
          {act.mode === "copy" ? (
            <div className="flex flex-col items-end gap-1">
              <CopyCmd cmd={act.command} compact />
              <span className="text-2xs text-faint">
                No one-click action wired yet{act.reason ? ` — ${act.reason}` : ""}.
              </span>
            </div>
          ) : act.mode === "batch-enumerable" ? null : (
            <GovernedAct act={act} mutationsEnabled={mutationsEnabled} registerTrigger={registerTrigger} />
          )}
        </div>
      </div>

      {act.mode === "batch-enumerable" ? (
        <div className="mt-3">
          <EnumerableApply
            area={act.area}
            members={act.members}
            excluded={excluded}
            cursor={memberCursor}
            onToggle={onToggleMember}
            mutationsEnabled={mutationsEnabled}
            registerTrigger={registerTrigger}
          />
        </div>
      ) : (
        <ReadonlyMembers members={group.members} atomic={act.mode === "batch-atomic"} />
      )}

      {group.evidence.length && evidenceOpen ? (
        <ul className="mt-3 flex flex-col gap-0.5 border-t border-hair pt-2">
          {group.evidence.map((ref, i) => (
            <li key={i} className="mono truncate text-2xs text-faint" title={ref}>
              {ref}
            </li>
          ))}
        </ul>
      ) : null}

      <code className="mono mt-2 block truncate text-2xs text-faint" title={group.sampleId}>
        {group.sampleId}
      </code>
    </article>
  );
}

/* ---------- the queue: keyboard triage + focus + aria-live ---------- */

export function DecideQueue({ groups, mutationsEnabled }: { groups: DecideGroup[]; mutationsEnabled: boolean }) {
  const [filter, setFilter] = useState("");
  const [focused, setFocused] = useState(0);
  const [evidenceOpen, setEvidenceOpen] = useState<Record<string, boolean>>({});
  const [excluded, setExcluded] = useState<Record<string, Set<string>>>({});
  const [memberCursor, setMemberCursor] = useState<Record<string, number>>({});
  const [detailKey, setDetailKey] = useState<string | null>(null);

  const filterRef = useRef<HTMLInputElement | null>(null);
  const cardRefs = useRef<(HTMLElement | null)[]>([]);
  const triggers = useRef<Map<string, () => void>>(new Map());

  const visible = useMemo(() => {
    const q = filter.trim().toLowerCase();
    if (!q) return groups;
    return groups.filter((g) => `${g.title} ${g.areaLabel} ${g.body}`.toLowerCase().includes(q));
  }, [groups, filter]);

  useEffect(() => {
    if (focused > visible.length - 1) setFocused(Math.max(0, visible.length - 1));
  }, [visible.length, focused]);

  const focusCard = useCallback((i: number) => {
    const clamped = Math.max(0, Math.min(i, cardRefs.current.length - 1));
    setFocused(clamped);
    cardRefs.current[clamped]?.focus();
  }, []);

  const toggleEvidence = useCallback((key: string) => {
    setEvidenceOpen((prev) => ({ ...prev, [key]: !prev[key] }));
  }, []);

  const toggleMember = useCallback((key: string, id: string) => {
    setExcluded((prev) => {
      const next = new Set(prev[key] ?? []);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return { ...prev, [key]: next };
    });
  }, []);

  const moveMemberCursor = useCallback((group: DecideGroup, delta: number) => {
    if (group.act.mode !== "batch-enumerable") return;
    const max = group.act.members.length - 1;
    setMemberCursor((prev) => {
      const cur = prev[group.key] ?? 0;
      return { ...prev, [group.key]: Math.max(0, Math.min(max, cur + delta)) };
    });
  }, []);

  // '/' focuses the filter from anywhere (except while already typing).
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const el = e.target as HTMLElement | null;
      const typing = !!el?.closest("input, textarea, select, [contenteditable=true]");
      if (e.key === "/" && !typing) {
        e.preventDefault();
        filterRef.current?.focus();
        filterRef.current?.select();
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  function onCardKey(e: React.KeyboardEvent, index: number, group: DecideGroup) {
    // Only triage when the card CONTAINER itself is focused — a focused child
    // button/checkbox handles its own keys (so `a` never double-fires).
    if (e.target !== e.currentTarget) return;
    switch (e.key) {
      case "j":
        e.preventDefault();
        focusCard(index + 1);
        break;
      case "k":
        e.preventDefault();
        focusCard(index - 1);
        break;
      case "a":
        e.preventDefault();
        triggers.current.get(group.key)?.(); // opens ConfirmDialog when needed — never auto-fires
        break;
      case "e":
        e.preventDefault();
        toggleEvidence(group.key);
        break;
      case "x":
        if (group.act.mode === "batch-enumerable") {
          e.preventDefault();
          const cursor = memberCursor[group.key] ?? 0;
          const member = group.act.members[cursor];
          if (member) toggleMember(group.key, member.id);
        }
        break;
      case "ArrowDown":
        if (group.act.mode === "batch-enumerable") {
          e.preventDefault();
          moveMemberCursor(group, 1);
        }
        break;
      case "ArrowUp":
        if (group.act.mode === "batch-enumerable") {
          e.preventDefault();
          moveMemberCursor(group, -1);
        }
        break;
      case "Enter":
      case " ":
        e.preventDefault();
        setDetailKey(group.key);
        break;
      default:
        break;
    }
  }

  const detail = detailKey ? groups.find((g) => g.key === detailKey) ?? null : null;
  const liveMessage = visible.length
    ? `${visible.length} decision${visible.length === 1 ? "" : "s"} need you. Focused ${Math.min(focused + 1, visible.length)} of ${visible.length}: ${visible[Math.min(focused, visible.length - 1)]?.title ?? ""}`
    : "Nothing waits on you.";

  return (
    <div className="flex flex-col gap-3">
      <div aria-live="polite" className="sr-only">
        {liveMessage}
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <input
          ref={filterRef}
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder="Filter decisions…  ( / )"
          aria-label="Filter decisions"
          className="w-full max-w-xs rounded-md border border-hair bg-panel px-3 py-1.5 text-sm text-ink outline-none focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-work sm:w-64"
        />
        <span className="text-2xs text-faint">
          <kbd className="mono">j</kbd>/<kbd className="mono">k</kbd> move · <kbd className="mono">a</kbd> act ·{" "}
          <kbd className="mono">e</kbd> evidence · <kbd className="mono">x</kbd> exclude ·{" "}
          <kbd className="mono">Enter</kbd> details
        </span>
      </div>

      {visible.length ? (
        <div className="flex flex-col gap-3">
          {visible.map((group, index) => (
            <DecideCard
              key={group.key}
              group={group}
              index={index}
              focused={focused === index}
              evidenceOpen={!!evidenceOpen[group.key]}
              excluded={excluded[group.key] ?? new Set()}
              memberCursor={memberCursor[group.key] ?? 0}
              mutationsEnabled={mutationsEnabled}
              cardRef={(el) => {
                cardRefs.current[index] = el;
              }}
              onFocus={() => setFocused(index)}
              onKey={(e) => onCardKey(e, index, group)}
              onOpenDetail={() => setDetailKey(group.key)}
              onToggleMember={(id) => toggleMember(group.key, id)}
              registerTrigger={(fn) => {
                if (fn) triggers.current.set(group.key, fn);
                else triggers.current.delete(group.key);
              }}
            />
          ))}
        </div>
      ) : (
        <p className="rounded-lg border border-dashed border-hair bg-page px-4 py-6 text-center text-sm text-muted">
          No decision matches “{filter}”.
        </p>
      )}

      <Drawer
        open={!!detail}
        onOpenChange={(o) => !o && setDetailKey(null)}
        title={detail?.title ?? ""}
        meta={detail ? `${detail.areaLabel} · ${detail.sampleId}` : ""}
      >
        {detail ? (
          <div className="flex flex-col gap-4 text-sm">
            <p className="text-muted">{detail.areaPlain}</p>
            {detail.body ? <p className="leading-relaxed text-ink">{detail.body}</p> : null}
            {detail.consequences ? (
              <ConsequencePair approve={detail.consequences.approve} hold={detail.consequences.hold} />
            ) : null}
            {detail.act.mode !== "copy" && "argv" in detail.act && detail.act.argv?.length ? (
              <div>
                <div className="text-2xs font-bold uppercase tracking-wide text-faint">Command</div>
                <pre className="mono mt-1 overflow-x-auto rounded-md border border-hair bg-soft px-3 py-2 text-2xs text-muted">
                  compound {detail.act.argv.join(" ")}
                </pre>
              </div>
            ) : detail.act.mode === "copy" ? (
              <div>
                <div className="text-2xs font-bold uppercase tracking-wide text-faint">Command (copy)</div>
                <pre className="mono mt-1 overflow-x-auto rounded-md border border-hair bg-soft px-3 py-2 text-2xs text-muted">
                  {detail.act.command}
                </pre>
              </div>
            ) : null}
            {detail.members.length > 1 ? (
              <div>
                <div className="text-2xs font-bold uppercase tracking-wide text-faint">
                  Members · {detail.members.length}
                </div>
                <ul className="mt-1 flex flex-col gap-1">
                  {detail.members.slice(0, 80).map((m) => (
                    <li key={m.id} className="mono truncate text-2xs text-faint" title={m.id}>
                      {m.label}
                    </li>
                  ))}
                </ul>
              </div>
            ) : null}
            {detail.evidence.length ? (
              <div>
                <div className="text-2xs font-bold uppercase tracking-wide text-faint">
                  Evidence · {detail.evidence.length}
                </div>
                <ul className="mt-1 flex flex-col gap-0.5">
                  {detail.evidence.map((ref, i) => (
                    <li key={i} className="mono truncate text-2xs text-faint" title={ref}>
                      {ref}
                    </li>
                  ))}
                </ul>
              </div>
            ) : null}
            <FreshnessChip generatedAt={detail.freshness.generatedAt} source={detail.freshness.source} />
          </div>
        ) : null}
      </Drawer>
    </div>
  );
}
