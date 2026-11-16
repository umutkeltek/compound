/*
 * Sources: one plain status per connection (`compound source connections
 * --json`), and what happens next, in words.
 *
 * The CLI's own labels disagreed with the dates it showed: a daily feed 27
 * days old read "observed" (green) while one 3 days old read "stale" (red),
 * because its stale rule looks at intake rows, not at the date the page
 * shows. Here the status is derived from the date shown, with one window for
 * every feed: the CLI's own intake window (72 hours). Imports are one-off and
 * make no freshness claim. Nothing here invents a cadence per source.
 *
 * Pure and client-safe: no reads, no `node:` imports.
 */
import { timestampMs } from "@/lib/work-state";

/** A feed with no new data for longer than this is out of date (the CLI's intake window). */
export const SOURCE_STALE_AFTER_HOURS = 72;

export type SourceStatusKey = "out_of_date" | "needs_setup" | "age_unknown" | "current" | "imported" | "not_connected";

/** The order statuses are listed in: what may need him first. */
export const SOURCE_STATUS_ORDER: readonly SourceStatusKey[] = ["out_of_date", "needs_setup", "age_unknown", "current", "imported", "not_connected"];

export const SOURCE_STATUS_WORDS: Record<SourceStatusKey, { label: string; plural: string; tone: "good" | "seam" | "muted" }> = {
  out_of_date: { label: "Out of date", plural: "out of date", tone: "seam" },
  needs_setup: { label: "Needs setup", plural: "need setup", tone: "seam" },
  age_unknown: { label: "Age unknown", plural: "age unknown", tone: "muted" },
  current: { label: "Up to date", plural: "up to date", tone: "good" },
  imported: { label: "Imported", plural: "imported", tone: "muted" },
  not_connected: { label: "Not connected", plural: "not connected", tone: "muted" },
};

export function isSourceStatusKey(value: string): value is SourceStatusKey {
  return (SOURCE_STATUS_ORDER as readonly string[]).includes(value);
}

type Row = Record<string, unknown>;
const s = (v: unknown): string => (typeof v === "string" ? v : "");
const list = (v: unknown): string[] => (Array.isArray(v) ? v.map((x) => (typeof x === "string" ? x : String(x))).filter(Boolean) : []);
const count = (v: unknown): number | null => (typeof v === "number" && Number.isFinite(v) && v >= 0 ? v : null);

/** "just now", "3 h ago", "27 days ago". */
export function sourceAge(ms: number): string {
  if (ms < 60 * 60_000) return ms < 60_000 ? "just now" : `${Math.floor(ms / 60_000)} min ago`;
  const hours = Math.floor(ms / 3_600_000);
  if (hours < 48) return `${hours} h ago`;
  return `${Math.floor(hours / 24)} days ago`;
}

/** "80 hours", "27 days": how long a feed has gone without new data. */
export function sourceSpan(ms: number): string {
  const hours = Math.floor(ms / 3_600_000);
  return hours < 48 ? `${hours} hours` : `${Math.floor(hours / 24)} days`;
}

/** What a source brings in, in everyday words (the projection's `produces`). */
const PRODUCES: [RegExp, string][] = [
  [/^memory candidates?$/i, "proposed memories"],
  [/^candidate evidence$/i, "evidence to review"],
  [/^review candidates?$/i, "items to review"],
  [/^skill candidates?$/i, "possible new skills"],
  [/^product loop evidence$/i, "app results"],
  [/^product signals?$/i, "app numbers"],
  [/^source observations?$/i, "observations"],
];

export function plainProduces(item: string): string {
  for (const [re, words] of PRODUCES) if (re.test(item.trim())) return words;
  return item.replace(/[_-]+/g, " ").trim();
}

function shellArg(arg: string): string {
  return /^[A-Za-z0-9_./:=@+-]+$/.test(arg) ? arg : JSON.stringify(arg);
}

export type SourceView = {
  id: string;
  label: string;
  status: { key: SourceStatusKey; label: string; tone: "good" | "seam" | "muted"; detail: string };
  /** a private key or account must be set on this computer */
  needsPrivateSetting: boolean;
  /** availability in words, only when the projection knows it */
  availability: string | null;
  rows: number | null;
  produces: string[];
  /** what happens next and who handles it, in one sentence */
  next: string;
  /** where its data shows up, when that is known */
  link: { href: string; label: string } | null;
  /** read-only commands that look at the source (they do not refresh it) */
  inspectCommands: string[];
  /** intake cards an operator-ready console may plan work for */
  intakeCards: { id: string; title: string }[];
  recordedNextStep: string;
  sourceRef: string;
  sourceKinds: string[];
};

const AVAILABILITY: Record<string, string> = {
  healthy: "Connected and working",
  configured: "Set up, not yet checked",
  failing: "Connection failing",
};

/**
 * One connection as the page shows it. `appHref` turns a `product:` id into
 * the app's page (passed in so this file stays free of route rules).
 */
export function sourceView(connection: Row, nowMs: number, appHref: (app: string) => string): SourceView {
  const id = s(connection.id);
  const evidence = s(connection.evidence_state);
  const availabilityRaw = s(connection.availability);
  const needsPrivateSetting = connection.private_setup_needed === true || availabilityRaw === "needs_private_config";
  const rows = count(connection.row_count);
  const lastSeen = timestampMs(connection.last_seen);
  const age = lastSeen === null ? null : Math.max(0, nowMs - lastSeen);
  const produces = list(connection.produces).map(plainProduces);
  const cards = Array.isArray(connection.latest_cards) ? (connection.latest_cards as Row[]) : [];

  let key: SourceStatusKey;
  let detail: string;
  if (evidence === "imported") {
    key = "imported";
    detail = age === null ? "Imported; when is not recorded." : `Last import ${sourceAge(age)}.`;
  } else if (evidence === "never_seen" || (lastSeen === null && !rows)) {
    key = needsPrivateSetting ? "needs_setup" : "not_connected";
    detail = needsPrivateSetting ? "Needs a private setting (a key or an account) on this computer." : "Has never brought in any data.";
  } else if (age === null) {
    key = "age_unknown";
    detail = "Has data, but when it last arrived is not recorded.";
  } else if (age <= SOURCE_STALE_AFTER_HOURS * 3_600_000) {
    key = "current";
    detail = `Last data ${sourceAge(age)}.`;
  } else {
    key = "out_of_date";
    detail = `No new data for ${sourceSpan(age)}.`;
  }

  const next =
    key === "out_of_date"
      ? needsPrivateSetting
        ? "Nothing refreshes it yet: it needs a private setting on this computer, which an agent adds with you."
        : "Nothing refreshes it on its own yet: an agent has to bring in new data."
      : key === "needs_setup"
        ? "Nothing yet: an agent sets it up with you when you want this source."
        : key === "not_connected"
          ? "Nothing yet: this source is not connected."
          : "Nothing needs you here.";

  // Where its data shows up: the app a card names, else the app pages when
  // it brings in app numbers. Nothing is linked when neither is known.
  const product = cards.flatMap((card) => list(card.command)).find((arg) => /^product:[A-Za-z0-9_-]+$/.test(arg)) ?? "";
  const link = product
    ? { href: appHref(product), label: "See this app's numbers" }
    : produces.includes("app numbers")
      ? { href: appHref(""), label: "See app numbers" }
      : null;

  return {
    id,
    label: s(connection.label) || id || "Unnamed source",
    status: { key, label: SOURCE_STATUS_WORDS[key].label, tone: SOURCE_STATUS_WORDS[key].tone, detail },
    needsPrivateSetting,
    availability: AVAILABILITY[availabilityRaw] ?? null,
    rows,
    produces,
    next,
    link,
    inspectCommands: [...new Set(cards.map((card) => list(card.command).map(shellArg).join(" ")).filter(Boolean))],
    intakeCards: cards
      .map((card) => ({ id: s(card.id), title: s(card.title) }))
      .filter((card) => /^sicard_[a-z0-9]+$/.test(card.id)),
    recordedNextStep: s(connection.next_safe_action),
    sourceRef: s(connection.source_ref),
    sourceKinds: list(connection.source_kinds),
  };
}

/** Status order first, then name. */
export function compareSources(a: SourceView, b: SourceView): number {
  const rank = (v: SourceView) => SOURCE_STATUS_ORDER.indexOf(v.status.key);
  return rank(a) - rank(b) || a.label.localeCompare(b.label);
}
