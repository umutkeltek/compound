"use client";

import Link from "next/link";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";

import { CopyCmd } from "@/components/CopyCmd";
import { FreshnessChip } from "@/components/kit";
import type { DecideDomain, DecideGroup, DecideMember, DecisionAbout, DecisionListedItem, HeldWorkRow } from "@/lib/decide-model";
import { ago } from "@/lib/time";
import { humanizeProductId } from "@/lib/app-identity";
import { plainArea, plainText, productTitle } from "@/lib/plain-words";
import { PanelProofContinuity } from "./PanelProofContinuity";
import { EvidenceReference } from "@/components/memory/EvidenceReference";

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
  return plainText(
    value
      .replace(/\bclaude_code\b/gi, "Claude Code")
      .replace(/\bswitchback_api\b/gi, "Switchback API"),
  );
}

/**
 * A title in plain words, as Today shows it: a title that starts with a
 * product id leads with the product's name (named as the apps pages name
 * it), and the id comes back apart for small secondary text.
 */
function titled(value: string): { text: string; id: string } {
  return productTitle(operatorText(value), humanizeProductId);
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

/* ---------- enumerable batch: per-member checkboxes + exclusion ---------- */

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
          <li key={m.id}>
            {m.href ? (
              <Link
                href={m.href}
                className="block min-w-0 truncate text-2xs text-muted underline-offset-2 hover:text-ink hover:underline"
                title={m.id}
              >
                {m.label}
              </Link>
            ) : (
              <span className="block min-w-0 truncate text-2xs text-muted" title={m.id}>
                {m.label}
              </span>
            )}
          </li>
        ))}
      </ul>
      {members.length > 60 ? (
        <p className="mt-1 text-2xs text-faint">+{members.length - 60} more not shown.</p>
      ) : null}
    </details>
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
      data-decision-row={group.key}
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
          <span className="truncate text-2xs font-bold uppercase text-faint" title={`${group.areaPlain} (${group.area})`}>{plainArea(group.area || group.areaLabel)}</span>
          <time
            dateTime={group.face.proposedAt || undefined}
            title={group.face.proposedAtLabel || "Proposal time unavailable"}
            className={`shrink-0 text-2xs tabular-nums ${group.old ? "font-semibold text-seam" : "text-faint"}`}
          >
            {group.headline?.waiting ? `waiting ${group.headline.waiting}` : group.ageLabel}
          </time>
        </span>
        {/* The row says what Today's row says (decide-model decisionHeadline):
         * the app or thing it is about and the record's own words. */}
        {group.headline?.about ? <span className="mt-1 block truncate text-xs font-semibold text-muted">{group.headline.about.label}</span> : null}
        <span className="mt-1 line-clamp-2 block text-sm font-semibold leading-snug text-ink">
          {group.headline?.subject || group.headline?.question || titled(group.objectText || group.title).text}
        </span>
        {group.headline?.needsWording ? <span className="mt-1 block text-2xs text-seam">An agent must word this first</span> : null}
        {group.headline?.countLabel ? <span className="mt-1 block text-2xs text-faint">{group.headline.countLabel}</span>
          : group.count > 1 ? <span className="mt-1 block text-2xs text-faint">{group.count} items</span> : null}
      </span>
    </button>
  );
}

function EvidencePanel({ group }: { group: DecideGroup }) {
  const rows = (
    <div className="divide-y divide-hair border-y border-hair">
      {group.evidence.map((evidence, index) => (
        <div key={`${evidence.ref}:${index}`} className="grid gap-1 py-2.5">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <span className="text-xs font-semibold text-ink">{evidence.label}</span>
            {evidence.sourceKind ? <span className="rounded-pill border border-hair bg-soft px-2 py-0.5 text-2xs text-faint">{evidence.sourceKind}</span> : null}
          </div>
          {evidence.summary ? <p className="text-xs leading-relaxed text-muted">{evidence.summary}</p> : null}
          {evidence.ref ? <EvidenceReference reference={evidence.ref} /> : null}
          {evidence.openCommand ? <details className="text-xs text-muted"><summary className="cursor-pointer">Source command</summary><div className="mt-2"><CopyCmd cmd={evidence.openCommand} compact /></div></details> : null}
        </div>
      ))}
    </div>
  );

  return (
    <div className="grid gap-3">
      {/*
       * One freshness stamp here, not two: the decision's own proposed-at
       * age already appears once, clearly labeled, in the card header above
       * ("Proposed … (age)"). Repeating a bare, unlabeled age+"stale" badge
       * next to the live/stale FreshnessChip read as two contradictory
       * freshness claims on one row — so only the projection-freshness
       * chip (what this Evidence tab is actually about) lives here.
       */}
      <div className="flex flex-wrap items-center gap-3 rounded-md bg-soft px-3 py-2">
        <code className="mono text-2xs text-faint" title={group.sampleId}>{group.sampleId}</code>
        <span className="inline-flex items-center gap-1.5">
          <span className="text-faint">List read</span>
          <FreshnessChip generatedAt={group.face.freshness.generatedAt} source={group.face.freshness.source} />
        </span>
      </div>
      {group.evidence.length ? rows : (
        <p className="rounded-md border border-dashed border-hair px-3 py-6 text-center text-xs text-muted">
          No other evidence is attached to this decision. <Link href="/sources" className="font-semibold text-work hover:underline">Check evidence sources →</Link>
        </p>
      )}
    </div>
  );
}

function HistoryPanel({ group }: { group: DecideGroup }) {
  return (
    <div className="grid gap-3">
      <ol className="grid gap-2" aria-label="Decision history">
        <li className="grid grid-cols-[auto_1fr] gap-3 rounded-md border border-hair bg-soft px-3 py-2.5">
          <span aria-hidden className="mt-1 h-2 w-2 rounded-full bg-seam" />
          <div>
            <div className="text-xs font-semibold text-ink">Raised for review</div>
            <p className="mt-0.5 text-xs text-muted">
              {group.face.proposedAtLabel ? (
                <><time dateTime={group.face.proposedAt}>{group.face.proposedAtLabel}</time> ({group.ageLabel}).</>
              ) : `Proposal time unavailable (${group.ageLabel}).`}
            </p>
          </div>
        </li>
      </ol>
      {group.members.length > 1 ? <ReadonlyMembers members={group.members} atomic={group.act.mode === "batch-atomic"} /> : null}
    </div>
  );
}

function AboutLink({ about }: { about: DecisionAbout }) {
  return about.href ? (
    <Link href={about.href} className="font-semibold text-work hover:underline">{about.label}</Link>
  ) : (
    <span className="font-semibold text-muted">{about.label}</span>
  );
}

/** Held work, inline: each item with its title, app, recorded reason and since when, linking its page. */
function HeldWorkList({ rows }: { rows: HeldWorkRow[] }) {
  return (
    <ul className="mt-3 divide-y divide-hair rounded-md border border-hair" aria-label="Held work items">
      {rows.map((row) => (
        <li key={row.id} className="grid gap-0.5 px-3 py-2.5">
          <Link href={row.href} className="text-sm font-semibold text-work hover:underline">{row.title} →</Link>
          <span className="text-xs text-muted">
            {row.app ? <><AboutLink about={row.app} /> · </> : null}
            {row.reason || "No reason was recorded for the hold."}
          </span>
          {row.since ? <span className="text-2xs text-faint">On hold since {ago(row.since)}</span> : null}
        </li>
      ))}
    </ul>
  );
}

/** The items the waiting list names inside a decision, each with the app or thing it is about. */
function ListedItems({ items }: { items: DecisionListedItem[] }) {
  return (
    <ul className="mt-3 max-w-read divide-y divide-hair rounded-md border border-hair" aria-label="What it covers">
      {items.slice(0, 25).map((item) => (
        <li key={item.id} id={`decision-item-${item.id}`} className="grid scroll-mt-24 gap-0.5 px-3 py-2">
          {item.about ? <span className="text-xs"><AboutLink about={item.about} /></span> : null}
          {item.title ? <span className="line-clamp-2 text-sm text-ink">{item.title}</span> : null}
          {item.createdAt ? <span className="text-2xs text-faint">Since {ago(item.createdAt)}</span> : null}
        </li>
      ))}
      {items.length > 25 ? <li className="px-3 py-2 text-2xs text-faint">{items.length - 25} more are not shown here.</li> : null}
    </ul>
  );
}

/**
 * One decision as one card: the app or thing it is about, the question in
 * one sentence, what a yes and a no would cause, whether it can be undone and
 * what is recommended, from the kind's words (lib/decision-registry.ts) and
 * the record. What the record does not say is named once. There is no button:
 * how a decision moves today is said once at the top of the page. The record
 * behind it sits in a closed disclosure for agents.
 */
function DecisionDetail({ group }: { group: DecideGroup }) {
  const card = group.card;
  const rows: { label: string; text: string | null }[] = card ? [
    { label: "If you say yes", text: card.yes },
    { label: "If you say no", text: card.no },
    { label: "Can it be undone?", text: card.undo },
    { label: "Recommended", text: card.recommendation },
  ] : [];
  return (
    <article className="decision-focus overflow-hidden rounded-xl border border-hair bg-panel" data-decision-card={group.key}>
      <header className="px-4 py-4">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-2xs text-faint">
          <span className="font-bold uppercase">{plainArea(group.area || group.areaLabel)}</span>
          {card?.about ? <span>· {card.about.app ? "App " : ""}<AboutLink about={card.about} /></span> : null}
          <span>· waiting {group.headline?.waiting || group.ageLabel}</span>
          {group.headline?.countLabel ? <span>· {group.headline.countLabel}</span> : group.count > 1 ? <span>· {group.count} items</span> : null}
        </div>
        {card?.needsWording ? (
          <>
            {card.subject ? <p className="mt-3 max-w-read text-lg font-semibold leading-snug text-ink">{card.subject}</p> : null}
            <p className="mt-2 max-w-read text-sm text-seam">An agent must word this as a clear question first. Nothing is asked of you until then.</p>
          </>
        ) : (
          <>
            <h3 className="mt-3 max-w-read text-lg font-semibold leading-snug text-ink">{card?.question || titled(group.objectText || group.title).text}</h3>
            {card?.question && card.subject ? (
              <blockquote className="mt-2 max-w-read border-l-2 border-work-line pl-3 text-sm leading-relaxed text-muted">{card.subject}</blockquote>
            ) : null}
            {rows.some((row) => row.text) ? (
              <dl className="mt-3 grid max-w-read gap-2 text-sm">
                {rows.filter((row) => row.text).map((row) => (
                  <div key={row.label}>
                    <dt className="text-2xs font-bold uppercase tracking-wide text-faint">{row.label}</dt>
                    <dd className="mt-0.5 leading-relaxed text-ink">{row.text}</dd>
                  </div>
                ))}
              </dl>
            ) : null}
            {card?.silent ? <p className="mt-3 max-w-read text-xs text-faint">{card.silent}</p> : null}
            {card?.listed.length ? <ListedItems items={card.listed} /> : null}
            {card?.answerAt ? (
              <p className="mt-3 max-w-read text-sm">
                <Link href={card.answerAt.href} className="font-semibold text-work hover:underline">{card.answerAt.label} →</Link>
              </p>
            ) : null}
          </>
        )}
        {card?.perItem && group.heldWork?.length ? <HeldWorkList rows={group.heldWork} /> : null}
      </header>
      <details className="border-t border-hair px-4 py-3 text-xs text-muted">
        <summary className="cursor-pointer font-semibold">For agents: the record behind it</summary>
        <div className="mt-3 grid gap-4">
          <HistoryPanel group={group} />
          {group.evidence.length ? <EvidencePanel group={group} /> : null}
          <CopyCmd cmd={group.members[0]?.href?.startsWith("/work/") ? `compound work show ${group.members[0].id} --json` : `compound reviews --area '${group.area.replaceAll("'", "'\\''")}' --json`} compact />
        </div>
      </details>
    </article>
  );
}

/** The kind part of an area path ("source-control-cleanup/scr_…" → "source-control-cleanup"). */
function areaHead(area: string): string {
  return (area || "").split("/")[0].trim();
}

/**
 * What a row and its card show, and nothing else: search matches only text
 * he can see (the app, the record's words, the question, the kind).
 */
export function shownText(group: DecideGroup): string {
  const h = group.headline;
  return [h?.about?.label, h?.subject, h?.question, plainArea(group.area), h?.subject || h?.question ? "" : titled(group.objectText || group.title).text]
    .filter(Boolean).join(" ");
}

/** Decide's address with its search text and kind set; other parameters stay. */
export function decideFilterHref(pathname: string, currentSearch: string, filters: { q: string; area: string }): string {
  const params = new URLSearchParams(currentSearch);
  for (const [name, value] of [["q", filters.q], ["area", filters.area]] as const) {
    if (value) params.set(name, value);
    else params.delete(name);
  }
  const query = params.toString();
  return query ? `${pathname}?${query}` : pathname;
}

/**
 * The list's counter: of the decisions he can answer (what the page header
 * counts), how many show; the ones an agent must word first are named apart.
 */
export function decisionCounter(
  visible: ReadonlyArray<Pick<DecideGroup, "headline">>,
  groups: ReadonlyArray<Pick<DecideGroup, "headline">>,
): string {
  const answerable = (list: ReadonlyArray<Pick<DecideGroup, "headline">>) => list.filter((group) => !group.headline?.needsWording).length;
  const total = answerable(groups);
  const wording = groups.length - total;
  const base = `${answerable(visible)} of ${total} decision${total === 1 ? "" : "s"}`;
  return wording > 0 ? `${base} · ${wording} to be worded first` : base;
}

export function initialDecideIndex(
  groups: ReadonlyArray<Pick<DecideGroup, "key">>,
  requestedKey = "",
): number {
  const requestedIndex = requestedKey ? groups.findIndex((group) => group.key === requestedKey) : -1;
  return requestedIndex >= 0 ? requestedIndex : groups.length > 0 ? 0 : -1;
}

export function initialDecideKey(
  groups: ReadonlyArray<Pick<DecideGroup, "key">>,
  requestedKey = "",
): string {
  const index = initialDecideIndex(groups, requestedKey);
  return index >= 0 ? groups[index]?.key ?? "" : "";
}

export function decisionSelectionHref(pathname: string, currentSearch: string, decisionKey: string): string {
  const params = new URLSearchParams(currentSearch);
  if (decisionKey) params.set("decision", decisionKey);
  else params.delete("decision");
  const query = params.toString();
  return query ? `${pathname}?${query}` : pathname;
}

export function DecideQueue({
  groups,
  detailKeys,
  mutationsEnabled,
  keyboardShortcuts = true,
  initialSelectedKey = "",
}: {
  groups: DecideGroup[];
  /**
   * Keys of the groups sent WITH their detail (members, evidence, prepared
   * analysis). The page sends only the open decision's detail; opening
   * another one updates `?decision=` and the server sends that one's detail.
   * Omitted = every group carries its detail.
   */
  detailKeys?: string[];
  mutationsEnabled: boolean;
  keyboardShortcuts?: boolean;
  initialSelectedKey?: string;
}) {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  // Search text and kind live in the address (`q`, `area`; lib/links.ts
  // decideHref's names), so a reload, Back or a pasted link keeps them.
  const [filter, setFilter] = useState(() => searchParams.get("q") ?? "");
  // A kind in the address that no decision has is ignored, and the page says so.
  const [ignoredArea] = useState(() => {
    const asked = areaHead(searchParams.get("area") ?? "");
    return asked && !groups.some((group) => areaHead(group.area) === asked) ? asked : "";
  });
  const [areaFilter, setAreaFilter] = useState(() => {
    const asked = areaHead(searchParams.get("area") ?? "");
    return asked && groups.some((group) => areaHead(group.area) === asked) ? asked : "";
  });
  const [focused, setFocused] = useState(() => Math.max(0, initialDecideIndex(groups, initialSelectedKey)));
  const [selectedKey, setSelectedKey] = useState(() => initialDecideKey(groups, initialSelectedKey));

  const filterRef = useRef<HTMLInputElement | null>(null);
  const rowRefs = useRef<(HTMLButtonElement | null)[]>([]);
  const listRef = useRef<HTMLDivElement | null>(null);
  const detailRef = useRef<HTMLDivElement | null>(null);

  const replaceSelectionInURL = useCallback((key: string) => {
    if (!key || searchParams.get("decision") === key) return;
    router.replace(decisionSelectionHref(pathname, searchParams.toString(), key), { scroll: false });
  }, [pathname, router, searchParams]);

  const areas = useMemo(() => [...new Set(groups.map((group) => areaHead(group.area)))]
    .sort((a, b) => plainArea(a).localeCompare(plainArea(b))), [groups]);
  const visible = useMemo(() => {
    const query = filter.trim().toLowerCase();
    return groups.filter((group) => (!areaFilter || areaHead(group.area) === areaFilter) &&
      (!query || shownText(group).toLowerCase().includes(query)));
  }, [groups, filter, areaFilter]);

  // Write search and kind to the address after a typing pause, so each
  // keystroke neither rewrites the address nor reloads the open decision.
  useEffect(() => {
    const timer = window.setTimeout(() => {
      const href = decideFilterHref(pathname, searchParams.toString(), { q: filter.trim(), area: areaFilter });
      if (href !== `${pathname}${searchParams.toString() ? `?${searchParams.toString()}` : ""}`) router.replace(href, { scroll: false });
    }, 400);
    return () => window.clearTimeout(timer);
  }, [areaFilter, filter, pathname, router, searchParams]);

  useEffect(() => {
    const selectedIsCurrent = groups.some((group) => group.key === selectedKey);
    const selectedIsVisible = visible.some((group) => group.key === selectedKey);
    if (!selectedKey && visible[0]) {
      setSelectedKey(visible[0].key);
      replaceSelectionInURL(visible[0].key);
    } else if (selectedIsCurrent && !selectedIsVisible) {
      const nextKey = visible[0]?.key ?? "";
      setSelectedKey(nextKey);
      replaceSelectionInURL(nextKey);
    } else if (selectedKey && !selectedIsCurrent) {
      const nextKey = visible[0]?.key ?? "";
      setSelectedKey(nextKey);
      replaceSelectionInURL(nextKey);
    }
    if (focused > visible.length - 1) setFocused(Math.max(0, visible.length - 1));
  }, [focused, groups, replaceSelectionInURL, selectedKey, visible]);

  const requestedIndex = initialDecideIndex(groups, initialSelectedKey);
  const requestedExists = Boolean(
    initialSelectedKey && requestedIndex >= 0 && groups[requestedIndex]?.key === initialSelectedKey,
  );

  useEffect(() => {
    if (!requestedExists) return;
    setSelectedKey(initialSelectedKey);
    setFocused(requestedIndex);
    const timer = window.setTimeout(() => {
      if (!window.matchMedia("(max-width: 1023px)").matches) return;
      rowRefs.current[requestedIndex]?.scrollIntoView({ behavior: "auto", block: "nearest" });
      detailRef.current?.scrollIntoView({ behavior: "auto", block: "start" });
    }, 0);
    return () => window.clearTimeout(timer);
  }, [initialSelectedKey, requestedExists, requestedIndex]);

  useEffect(() => {
    // A link to a decision that is gone keeps its address, so the page can
    // say so; only a page opened with no decision writes the open one.
    if (!initialSelectedKey && selectedKey) replaceSelectionInURL(selectedKey);
  }, [initialSelectedKey, replaceSelectionInURL, selectedKey]);

  useEffect(() => {
    if (!keyboardShortcuts) return;
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
  }, [keyboardShortcuts]);

  const focusRow = useCallback((index: number) => {
    const clamped = Math.max(0, Math.min(index, rowRefs.current.length - 1));
    setFocused(clamped);
    rowRefs.current[clamped]?.focus();
  }, []);

  const select = useCallback((group: DecideGroup, index: number, scroll = false) => {
    setSelectedKey(group.key);
    setFocused(index);
    replaceSelectionInURL(group.key);
    if (scroll && window.matchMedia("(max-width: 1023px)").matches) {
      const behavior = window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth";
      window.setTimeout(() => detailRef.current?.scrollIntoView({ behavior, block: "start" }), 0);
    }
  }, [replaceSelectionInURL]);

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

  const selected = visible.find((group) => group.key === selectedKey) ?? visible[0] ?? null;
  // The queue only renders with groups, so "nothing selected" means the
  // filter hides them all - never that nothing waits.
  const liveMessage = selected ? `Selected decision: ${titled(selected.title).text}` : "No decision matches this filter.";
  const panelGroups = groups.filter((group) => Boolean(group.panel));
  // A group sent without its detail shows "Opening…" until the server sends
  // it (selecting it already replaced `?decision=`, which re-renders the page).
  const detailSet = detailKeys ? new Set(detailKeys) : null;
  const selectedHasDetail = !selected || !detailSet || detailSet.has(selected.key);

  // A selected Panel group gets the proof-continuity treatment once its
  // detail is here; until then the queue below shows "Opening…".
  if (selected?.panel) {
    if (selectedHasDetail) return (
      <PanelProofContinuity
        group={selected}
        panelGroups={panelGroups}
        totalDecisions={groups.length}
        mutationsEnabled={mutationsEnabled}
        onSelectGroup={(next) => {
          const nextIndex = Math.max(0, groups.findIndex((group) => group.key === next.key));
          setSelectedKey(next.key);
          setFocused(nextIndex);
          replaceSelectionInURL(next.key);
        }}
        onBack={() => {
          const next = groups.find((group) => !group.panel) ?? groups[0];
          if (!next) return;
          const nextIndex = Math.max(0, groups.findIndex((group) => group.key === next.key));
          setSelectedKey(next.key);
          setFocused(nextIndex);
          replaceSelectionInURL(next.key);
        }}
      />
    );
  }

  return (
    <div className="grid gap-3">
      <div aria-live="polite" className="sr-only">{liveMessage}</div>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <input
          ref={filterRef}
          name="decision-filter"
          autoComplete="off"
          value={filter}
          onChange={(event) => setFilter(event.target.value)}
          placeholder="Filter decisions…"
          aria-label="Filter decisions"
          className="w-full rounded-md border border-hair bg-panel px-3 py-2 text-sm text-ink outline-none focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-focus sm:max-w-xs"
        />
        <select aria-label="Filter by kind" value={areaFilter} onChange={(event) => setAreaFilter(event.target.value)} className="min-h-10 max-w-full rounded-md border border-hair bg-panel px-3 py-2 text-sm text-ink">
          <option value="">All kinds</option>{areas.map((area) => <option key={area} value={area}>{plainArea(area)} ({groups.filter((group) => areaHead(group.area) === area).length})</option>)}
        </select>
        {/* The counter counts what the header counts (decisions he can answer); the ones an agent must word first are named apart. */}
        <span className="mono text-2xs tabular-nums text-faint">{decisionCounter(visible, groups)}</span>
        {filter || areaFilter ? <button type="button" onClick={() => { setFilter(""); setAreaFilter(""); }} className="text-xs font-semibold text-work hover:underline">Clear filters</button> : null}
      </div>
      {ignoredArea ? (
        <p role="status" className="text-xs text-muted">No decision of the kind “{plainArea(ignoredArea)}” waits for you, so all kinds are shown.</p>
      ) : null}

      <div className="grid items-start gap-4 lg:grid-cols-5">
        <div ref={listRef} className="scroll-mt-16 max-h-[52vh] overflow-y-auto rounded-lg border border-hair bg-panel lg:col-span-2 lg:max-h-[70vh] lg:overflow-y-auto">
          {visible.length ? (
            <ol aria-label="Your decisions" className="list-none">
              {visible.map((group, index) => (
                <li key={group.key}>
                  <DecisionRow
                    group={group}
                    selected={selected?.key === group.key}
                    rowRef={(element) => { rowRefs.current[index] = element; }}
                    onActivate={() => select(group, index, true)}
                    onFocus={() => select(group, index)}
                    onKeyDown={(event) => onRowKey(event, index, group)}
                  />
                </li>
              ))}
            </ol>
          ) : (
            <p className="border-dashed px-4 py-8 text-center text-sm text-muted">
              {filter.trim() ? <>No decision matches “{filter}”.</> : <>No decision of this kind waits for you.</>}
            </p>
          )}
        </div>

        <div ref={detailRef} className="scroll-mt-20 lg:sticky lg:top-20 lg:col-span-3">
          {selected ? (
            <div className="grid gap-2">
              <button
                type="button"
                onClick={() => {
                  const behavior = window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth";
                  listRef.current?.scrollIntoView({ behavior, block: "start" });
                }}
                className="inline-flex min-h-11 w-fit items-center gap-1.5 rounded-md border border-hair bg-panel px-3 py-2 text-xs font-semibold text-muted hover:bg-soft lg:hidden"
              >
                <span aria-hidden>↑</span>
                All decisions
              </button>
              {!selectedHasDetail ? (
                <div role="status" className="rounded-xl border border-hair bg-panel px-4 py-6">
                  <div className="text-2xs font-bold uppercase text-faint">{plainArea(selected.area || selected.areaLabel)}</div>
                  <p className="mt-2 text-lg font-semibold leading-snug text-ink">{titled(selected.objectText || selected.title).text}</p>
                  <p className="mt-3 text-sm text-muted">Opening this decision…</p>
                </div>
              ) : (
              <DecisionDetail key={selected.key} group={selected} />
              )}
            </div>
          ) : (
            <div className="rounded-lg border border-dashed border-hair bg-page px-4 py-10 text-center text-sm text-muted">No decision matches this filter. Clear the search above, or <Link href="/work?lens=table&filter=blocked" className="font-semibold text-work hover:underline">review blocked work →</Link></div>
          )}
        </div>
      </div>
    </div>
  );
}
