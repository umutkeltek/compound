/**
 * The one title of a work item, for every surface: board card, list row,
 * trace drawer, item page, Decide, app pages. A record's title is, in order:
 * its stated `title`, its desired outcome, its success condition. A record
 * with none of those gets a title derived from what it does have (a packet
 * name, the words in a named id, its surface), so no surface ever falls
 * back to a generic placeholder such as "Work details".
 *
 * `workTitleFull` is the whole text (whitespace compacted). `workTitle` is
 * the same text shortened for cards and lists: some records carry a whole
 * agent prompt as their goal. Show the short form where space is tight and
 * keep the full form reachable (the item page heading, a tooltip).
 *
 * Client-safe and dependency-free: no reads, no imports.
 */

/** Longest title shown on cards, lists and the drawer; longer text is shortened. */
export const WORK_TITLE_MAX = 120;

/** Any work record shape: workbench rows, `work show`, `work list` rows. */
export type WorkTitleSource = { readonly [field: string]: unknown };

function text(value: unknown): string {
  return typeof value === "string" ? value.replace(/\s+/g, " ").trim() : "";
}

function sentenceCase(words: string): string {
  const trimmed = words.trim();
  return trimmed ? trimmed.charAt(0).toUpperCase() + trimmed.slice(1) : "";
}

/** "release-notes_v2" → "Release notes v2". */
function wordsOf(slug: string): string {
  return sentenceCase(slug.replace(/[-_]+/g, " ").replace(/\s+/g, " "));
}

/**
 * Words in a named id: `work_fixture_door_notes_20300109` → "Fixture
 * door notes". Hash parts and dates carry no words, so a hashed id
 * (`work_0a1b2c3d4e5f`) gives "".
 */
function idWords(id: string): string {
  const body = id.replace(/^work_/, "");
  const words = body
    .split(/[_\-.:]+/)
    .filter((part) => part && /[a-z]/i.test(part))
    .filter((part) => !/^\d{8}(t\d+)?$/i.test(part)) // 20261009, 20261009T02
    .filter((part) => !(/^[0-9a-f]{6,}$/i.test(part) && /\d/.test(part))); // hash
  return words.length ? sentenceCase(words.join(" ")) : "";
}

/** Words in a surface: the name in `ns:name`, the last folder of a path, or a plain slug. */
function surfaceWords(surface: string): string {
  if (!surface || surface === "-") return "";
  const tail = surface.includes("/") ? surface.split("/").filter(Boolean).pop() ?? "" : surface;
  const name = tail.includes(":") ? tail.split(":").filter(Boolean).pop() ?? "" : tail;
  return /[a-z]/i.test(name) ? wordsOf(name) : "";
}

/**
 * The full title text. Order: `title`, `desired_outcome`,
 * `success_predicate`; then, for a record with none of those, the packet
 * name (`packet:<name>` surface), the words in a named id, the surface's
 * words, and last "Work item <id>". Never a generic placeholder.
 */
export function workTitleFull(work: WorkTitleSource): string {
  const stated = text(work.title) || text(work.desired_outcome) || text(work.success_predicate);
  if (stated) return stated;
  const surface = text(work.canonical_surface);
  if (surface.startsWith("packet:")) {
    const packet = wordsOf(surface.slice("packet:".length));
    if (packet) return packet;
  }
  const id = text(work.work_id) || text(work.id);
  const derived = (id && idWords(id)) || surfaceWords(surface);
  if (derived) return derived;
  return id ? `Work item ${id}` : "Work item with no recorded name or id";
}

/**
 * Shorten a title to at most `max` characters. Prefers to end at a sentence
 * or clause boundary in the last half of the limit, then at a word; adds
 * "…" whenever text was dropped.
 */
export function shortenWorkTitle(full: string, max = WORK_TITLE_MAX): string {
  const compact = full.replace(/\s+/g, " ").trim();
  if (compact.length <= max) return compact;
  const room = compact.slice(0, max - 1);
  const floor = Math.floor(max / 2);
  const clause = Math.max(room.lastIndexOf(". "), room.lastIndexOf("; "), room.lastIndexOf("? "), room.lastIndexOf("! "));
  if (clause >= floor) return `${room.slice(0, clause).trimEnd()}…`;
  const space = room.lastIndexOf(" ");
  const cut = space >= floor ? room.slice(0, space) : room;
  return `${cut.replace(/[\s,;:.\-–—(]+$/, "")}…`;
}

/** The title for display: `workTitleFull`, shortened to `max` characters. */
export function workTitle(work: WorkTitleSource, max = WORK_TITLE_MAX): string {
  return shortenWorkTitle(workTitleFull(work), max);
}
