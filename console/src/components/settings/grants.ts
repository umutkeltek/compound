/**
 * Standing permissions in plain words (`compound policy
 * standing-grants-authority --json`): which ones are in force now, which
 * cannot be undone, which never expire, and which have run out. A grant is
 * in force while `not_before <= now < expires_at` (either end may be empty);
 * the policy owner applies the same window. Pure and clock-in: pass `nowMs`.
 */

type Json = Record<string, unknown>;

function text(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function time(value: string): number | null {
  if (!value) return null;
  const ms = Date.parse(value);
  return Number.isFinite(ms) ? ms : null;
}

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/** "15 Sep 2026" (UTC, so the day does not move with the viewer's zone); "" when unknown. */
export function readableDate(ms: number | null): string {
  if (ms === null) return "";
  const d = new Date(ms);
  return `${d.getUTCDate()} ${MONTHS[d.getUTCMonth()]} ${d.getUTCFullYear()}`;
}

export type GrantRow = {
  id: string;
  /** what it lets agents do, in words ("Shell operator scope") */
  what: string;
  /** the raw capability key, for small secondary text */
  capability: string;
  /** where it applies, raw ("" when everywhere the capability reaches) */
  target: string;
  /** cannot be undone once done */
  irreversible: boolean;
  /** no end date */
  neverExpires: boolean;
  /** "until 15 Sep 2026", "never expires", "expired 15 Sep 2026", "starts 1 Dec 2026" */
  when: string;
  /** the operator's own note on why, "" when none */
  reason: string;
  expiresMs: number | null;
};

export type GrantSummary = {
  /** in force now, most consequential first: cannot be undone and never expires, then cannot be undone, then never expires, then by end date */
  active: GrantRow[];
  /** not in force yet */
  upcoming: GrantRow[];
  /** ran out, newest first */
  expired: GrantRow[];
  /** "Agents may do 36 things without asking; 8 of them cannot be undone." */
  sentence: string;
};

function words(capability: string): string {
  const plain = capability.replace(/[._-]+/g, " ").replace(/\s+/g, " ").trim();
  return plain ? plain[0].toUpperCase() + plain.slice(1) : "Unnamed permission";
}

function rank(row: GrantRow): number {
  if (row.irreversible && row.neverExpires) return 0;
  if (row.irreversible) return 1;
  if (row.neverExpires) return 2;
  return 3;
}

export function summarizeGrants(grants: readonly unknown[], nowMs: number): GrantSummary {
  const active: GrantRow[] = [];
  const upcoming: GrantRow[] = [];
  const expired: GrantRow[] = [];
  grants.forEach((raw, index) => {
    const grant = (raw && typeof raw === "object" ? raw : {}) as Json;
    const capability = text(grant.capability) || text(grant.mutation_class);
    const expiresMs = time(text(grant.expires_at));
    const startsMs = time(text(grant.not_before));
    const ceiling = `${text(grant.reversibility_ceiling)} ${text(grant.reversibility)}`.toLowerCase();
    const row: GrantRow = {
      id: text(grant.id) || `grant-${index}`,
      what: words(capability),
      capability,
      target: text(grant.target_path) || text(grant.destination) || text(grant.desktop_surface),
      irreversible: /\birreversible\b/.test(ceiling),
      neverExpires: expiresMs === null,
      when: "",
      reason: text(grant.reason),
      expiresMs,
    };
    if (expiresMs !== null && expiresMs <= nowMs) {
      expired.push({ ...row, when: `expired ${readableDate(expiresMs)}` });
    } else if (startsMs !== null && startsMs > nowMs) {
      upcoming.push({ ...row, when: `starts ${readableDate(startsMs)}${expiresMs !== null ? `, until ${readableDate(expiresMs)}` : ""}` });
    } else {
      active.push({ ...row, when: expiresMs === null ? "never expires" : `until ${readableDate(expiresMs)}` });
    }
  });
  active.sort((a, b) => rank(a) - rank(b) || (a.expiresMs ?? Infinity) - (b.expiresMs ?? Infinity) || a.what.localeCompare(b.what));
  expired.sort((a, b) => (b.expiresMs ?? 0) - (a.expiresMs ?? 0));
  const irreversible = active.filter((row) => row.irreversible).length;
  const things = `${active.length.toLocaleString("en-US")} ${active.length === 1 ? "thing" : "things"}`;
  const sentence = active.length === 0
    ? "Agents may do nothing without asking: every change needs a fresh decision."
    : `Agents may do ${things} without asking; ${irreversible === 0 ? "all of them can be undone" : `${irreversible.toLocaleString("en-US")} of them cannot be undone`}.`;
  return { active, upcoming, expired, sentence };
}
