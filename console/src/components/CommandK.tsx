"use client";

import * as Dialog from "@radix-ui/react-dialog";
import { useRouter } from "next/navigation";
import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import type { SearchEntry, SearchGroup, SearchIndex, SearchKind } from "@/app/api/search/build";
import type { ReadState } from "@/lib/compound";
import { searchablePages } from "@/lib/experience";
import { decideHref, workListHref } from "@/lib/links";

/**
 * Search (⌘K, or the sidebar's Search box). It finds every page and tab by
 * name, earlier name and purpose (lib/experience.ts), every app by name and
 * alias, waiting decisions by the words Today shows, and open work by its
 * title. Apps, decisions and work come from /api/search, asked once when
 * search opens: the reads the pages already cache, so typing starts no read.
 * A read still loading says so. Arrows move, Enter opens, Escape closes, and
 * focus stays inside while it is open. Results link through lib/links.ts.
 */
export type SearchRow = SearchEntry | { kind: "more"; label: string; sub: string; href: string; terms: string };

export type SearchResultGroup = {
  id: "pages" | "apps" | "decisions" | "work";
  heading: string;
  /** Said under the heading when the read behind it has no copy yet or failed. */
  note: string;
  rows: SearchRow[];
};

const KIND_LABEL: Record<SearchKind | "more", string> = {
  page: "Page",
  view: "Quick view",
  app: "App",
  decision: "Decision",
  work: "Work",
  more: "",
};

/** Every page and tab, with its section and the words he may type for it. */
export const PAGE_ENTRIES: SearchEntry[] = searchablePages().map(({ section, page }) => ({
  kind: "page",
  label: page.label,
  sub: page.label === section.label ? page.description : `${section.label} · ${page.description}`,
  href: page.href,
  terms: [page.label, section.label, section.question, page.description, ...(page.aliases ?? []), ...(page.keywords ?? [])]
    .join(" ")
    .toLowerCase(),
}));

const LOADING: SearchGroup = { state: "loading", entries: [] };

function noteFor(state: ReadState, what: string): string {
  if (state === "loading") return `Still reading ${what}; results may be missing.`;
  if (state === "unreachable") return `Could not read ${what}; results may be missing.`;
  return "";
}

function score(entry: SearchEntry, query: string, tokens: string[]): number {
  const label = entry.label.toLowerCase();
  const hay = `${label} ${entry.sub.toLowerCase()} ${entry.terms}`;
  if (!tokens.every((token) => hay.includes(token))) return 0;
  if (label.startsWith(query)) return 4;
  if (label.includes(query)) return 3;
  if (tokens.every((token) => label.includes(token))) return 2;
  return 1;
}

function ranked(entries: SearchEntry[], query: string, tokens: string[]): SearchEntry[] {
  return entries
    .map((entry, index) => ({ entry, index, s: score(entry, query, tokens) }))
    .filter((hit) => hit.s > 0)
    .sort((a, b) => b.s - a.s || a.index - b.index)
    .map((hit) => hit.entry);
}

/** The groups search shows for a query. Pure, so the shell check can run it without a browser. */
export function searchResults(rawQuery: string, index: SearchIndex | null): SearchResultGroup[] {
  const query = rawQuery.trim().toLowerCase();
  const tokens = query.split(/\s+/).filter(Boolean);
  if (!query) return [{ id: "pages", heading: "Pages", note: "", rows: PAGE_ENTRIES }];

  const apps = index?.apps ?? LOADING;
  const decisions = index?.decisions ?? LOADING;
  const work = index?.work ?? LOADING;
  const groups: SearchResultGroup[] = [];

  const pages = ranked(PAGE_ENTRIES, query, tokens);
  if (pages.length) groups.push({ id: "pages", heading: "Pages", note: "", rows: pages.slice(0, 8) });

  const appHits = ranked(apps.entries, query, tokens);
  const appNote = noteFor(apps.state, "your apps");
  if (appHits.length || appNote) groups.push({ id: "apps", heading: "Apps", note: appNote, rows: appHits.slice(0, 8) });

  const decisionHits = ranked(decisions.entries, query, tokens);
  const decisionNote = noteFor(decisions.state, "what waits for you");
  if (decisionHits.length || decisionNote) {
    const rows: SearchRow[] = decisionHits.slice(0, 6);
    if (decisionHits.length > rows.length) {
      rows.push({ kind: "more", label: `See all ${decisionHits.length} matching decisions`, sub: "", href: decideHref({ q: rawQuery.trim() }), terms: "" });
    }
    groups.push({ id: "decisions", heading: "Decisions", note: decisionNote, rows });
  }

  const workHits = ranked(work.entries, query, tokens);
  const workNote = noteFor(work.state, "open work");
  if (workHits.length || workNote) {
    const rows: SearchRow[] = workHits.slice(0, 8);
    if (workHits.length > rows.length) {
      rows.push({ kind: "more", label: `See all ${workHits.length} matching work items`, sub: "", href: workListHref({ lens: "table", filter: "all", q: rawQuery.trim() }), terms: "" });
    }
    groups.push({ id: "work", heading: "Work", note: workNote, rows });
  }
  return groups;
}

function anyLoading(index: SearchIndex | null): boolean {
  return !index || [index.apps, index.decisions, index.work].some((group) => group.state === "loading");
}

export function CommandK() {
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState("");
  const [active, setActive] = useState(0);
  const [index, setIndex] = useState<SearchIndex | null>(null);
  const rowRefs = useRef<(HTMLAnchorElement | null)[]>([]);

  useEffect(() => {
    function onKey(e: globalThis.KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setOpen((o) => !o);
        setQ("");
      }
    }
    function onOpen() {
      setOpen(true);
      setQ("");
    }
    window.addEventListener("keydown", onKey);
    window.addEventListener("compound:command-k", onOpen);
    return () => {
      window.removeEventListener("keydown", onKey);
      window.removeEventListener("compound:command-k", onOpen);
    };
  }, []);

  // Ask for the index when search opens; while a read is still loading, ask
  // again every few seconds until it lands or search closes.
  useEffect(() => {
    if (!open) return;
    let alive = true;
    let timer: ReturnType<typeof setTimeout> | null = null;
    const load = () => {
      fetch("/api/search", { cache: "no-store" })
        .then((response) => (response.ok ? (response.json() as Promise<SearchIndex>) : null))
        .then((next) => {
          if (!alive) return;
          if (next) setIndex(next);
          if (!next || anyLoading(next)) timer = setTimeout(load, 4_000);
        })
        .catch(() => {
          if (alive) timer = setTimeout(load, 4_000);
        });
    };
    load();
    return () => {
      alive = false;
      if (timer) clearTimeout(timer);
    };
  }, [open]);

  const groups = useMemo(() => searchResults(q, index), [q, index]);
  const flat = useMemo(() => groups.flatMap((group) => group.rows), [groups]);

  useEffect(() => setActive(0), [q]);
  useEffect(() => {
    rowRefs.current[active]?.scrollIntoView({ block: "nearest" });
  }, [active]);

  function go(row: SearchRow | undefined) {
    if (!row) return;
    setOpen(false);
    router.push(row.href);
  }

  function onInputKey(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setActive((i) => (flat.length ? (i + 1) % flat.length : 0));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive((i) => (flat.length ? (i - 1 + flat.length) % flat.length : 0));
    } else if (e.key === "Enter") {
      e.preventDefault();
      go(flat[active]);
    }
  }

  let position = -1;
  return (
    <Dialog.Root open={open} onOpenChange={setOpen}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-50 bg-scrim backdrop-blur-sm motion-reduce:backdrop-blur-none" />
        <Dialog.Content
          className="fixed left-1/2 top-[14vh] z-50 flex w-[600px] max-w-[92vw] -translate-x-1/2 flex-col overflow-hidden rounded-xl border border-hair bg-panel shadow-lg focus:outline-none"
          aria-describedby="search-help"
        >
          <Dialog.Title className="sr-only">Search</Dialog.Title>
          <input
            autoFocus
            value={q}
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={onInputKey}
            role="combobox"
            aria-expanded="true"
            aria-controls="search-results"
            aria-autocomplete="list"
            aria-activedescendant={flat.length ? `search-row-${active}` : undefined}
            aria-label="Search"
            placeholder="Find a page, an app, a piece of work or a decision…"
            className="w-full border-b border-hair bg-transparent px-4 py-3 text-md text-ink outline-none placeholder:text-faint"
          />
          <div id="search-results" role="listbox" aria-label="Results" className="max-h-[50vh] overflow-y-auto p-1.5">
            {groups.map((group) => (
              <div key={group.id} role="group" aria-label={group.heading} className="pb-1">
                <div className="px-3 pb-1 pt-2 text-2xs font-semibold uppercase tracking-wide text-faint">{group.heading}</div>
                {group.note ? <div className="px-3 pb-1 text-xs text-muted">{group.note}</div> : null}
                {group.rows.map((row) => {
                  position += 1;
                  const at = position;
                  const selected = at === active;
                  return (
                    <a
                      key={`${group.id}-${row.href}-${at}`}
                      id={`search-row-${at}`}
                      ref={(el) => {
                        rowRefs.current[at] = el;
                      }}
                      href={row.href}
                      role="option"
                      aria-selected={selected}
                      data-kind={row.kind}
                      onMouseMove={() => setActive(at)}
                      onClick={(e) => {
                        if (e.metaKey || e.ctrlKey || e.shiftKey) return;
                        e.preventDefault();
                        go(row);
                      }}
                      className={`flex w-full items-center justify-between gap-3 rounded-lg px-3 py-2 text-left ${selected ? "bg-soft" : ""}`}
                    >
                      <span className="min-w-0">
                        <span className={`block truncate text-sm ${row.kind === "more" ? "font-medium text-work" : "font-medium text-ink"}`}>{row.label}</span>
                        {row.sub ? <span className="block truncate text-xs text-muted">{row.sub}</span> : null}
                      </span>
                      {KIND_LABEL[row.kind] ? (
                        <span className="shrink-0 rounded-pill border border-hair px-2 py-0.5 text-2xs text-faint">{KIND_LABEL[row.kind]}</span>
                      ) : null}
                    </a>
                  );
                })}
              </div>
            ))}
            {!flat.length && !groups.some((group) => group.note) ? (
              <div className="px-3 py-6 text-center text-sm text-muted">Nothing matches “{q.trim()}”.</div>
            ) : null}
          </div>
          <div id="search-help" className="border-t border-hair px-4 py-2 text-2xs text-faint">
            ↑ ↓ to move · Enter to open · Esc to close
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
