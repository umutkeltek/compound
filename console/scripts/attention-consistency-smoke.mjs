import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";

/*
 * Phase 2 acceptance: ONE attention projection feeds every "what needs you"
 * surface. This smoke proves the nav Decide badge, the briefing pill, and the
 * Reviews needs-you band cannot silently diverge, by cross-checking the two
 * live projections a running console reads:
 *
 *   (a) `compound attention --json` (nav badge + briefing + Reviews ordering)
 *   (b) `compound reviews --json`   (what the Reviews page renders)
 *
 * It asserts:
 *   1. counts.needs_you_groups equals the actual number of needs_you groups in
 *      the projection (the count field can't drift from the array).
 *   2. attention's reviews-source needs_you groups match, key-for-key, the
 *      needs_you groups `compound reviews --json` emits — so the page (reads
 *      reviews) and the nav/briefing (read attention) agree on the reviews slice.
 *   3. counts.needs_you_groups reconciles with the number of needs_you groups
 *      the Reviews page renders AFTER its client fallback safety net, with any
 *      delta fully explained by an explicit allowlist of reasons.
 *   4. every attention group whose action is not copy-only carries a well-formed
 *      argv starting with "compound".
 *
 * Run manually / by the orchestrator; nothing wires it into Go.
 *   COMPOUND_BIN=../compound COMPOUND_HOME=~/.compound bun scripts/attention-consistency-smoke.mjs
 */

const BIN = process.env.COMPOUND_BIN || "compound";
const EXTERNAL_AREA = "external-repo-intake";

function runJson(args) {
  const out = execFileSync(BIN, args, { encoding: "utf8", maxBuffer: 64 * 1024 * 1024 });
  const start = out.indexOf("{");
  assert.ok(start >= 0, `no JSON object in \`${BIN} ${args.join(" ")}\` output`);
  return JSON.parse(out.slice(start));
}

const str = (v) => (typeof v === "string" ? v : "");
const arr = (v) => (Array.isArray(v) ? v : []);
const num = (v) => (typeof v === "number" ? v : 0);

/* ---- replicate the console's review-data.ts grouping so the smoke reasons
       about exactly what the Reviews page renders (kept in sync by review) ---- */
const areaOf = (row) => str(row.area) || str(row.review_area);
const bandOf = (row) => {
  const b = str(row.band);
  return b === "watching" || b === "filed" ? b : "needs_you";
};
const idOf = (row) => str(row.id) || str(row.review_id);
function fallbackGroupKey(row) {
  const area = areaOf(row) || "review";
  const basis =
    str(row.group_key) ||
    str(row.context_label) ||
    str(row.operator_summary) ||
    str(row.next_action) ||
    idOf(row) ||
    "decision";
  return `${area}:${basis.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "").slice(0, 80)}`;
}

const attention = runJson(["attention", "--json"]);
const reviews = runJson(["reviews", "--json"]);

assert.equal(str(attention.schema_version), "compound.attention.v0.1", "unexpected attention schema");

const attnGroups = arr(attention.groups);
const attnNeedsYou = attnGroups.filter((g) => str(g.band) === "needs_you");
const A = num(attention.counts?.needs_you_groups);

/* ---- check 1: counts.needs_you_groups == actual needs_you groups ---- */
assert.equal(
  A,
  attnNeedsYou.length,
  `counts.needs_you_groups (${A}) != number of needs_you groups in .groups (${attnNeedsYou.length})`,
);

/* ---- source tally ---- */
const bySource = {};
for (const g of attnNeedsYou) {
  const s = str(g.source) || "unknown";
  bySource[s] = (bySource[s] ?? 0) + 1;
}

/* ---- check 2: attention reviews-source groups == reviews.json backend groups (key-for-key) ---- */
const attnReviewKeys = new Set(
  attnNeedsYou.filter((g) => str(g.source) === "reviews").map((g) => str(g.key)),
);
const revBackend = arr(reviews.groups).filter(
  (g) => bandOf(g) === "needs_you" && areaOf(g) !== EXTERNAL_AREA,
);
const revBackendKeys = new Set(revBackend.map((g) => str(g.key)));

const onlyInAttention = [...attnReviewKeys].filter((k) => !revBackendKeys.has(k));
const onlyInReviews = [...revBackendKeys].filter((k) => !attnReviewKeys.has(k));
assert.deepEqual(
  { onlyInAttention, onlyInReviews },
  { onlyInAttention: [], onlyInReviews: [] },
  "attention reviews-source needs_you groups drifted from `compound reviews --json` — the two projections must stay key-for-key identical on the reviews slice",
);

/* ---- reconstruct what the Reviews page renders (after the fallback safety net) ---- */
const covered = new Set([
  ...revBackend.flatMap((g) => arr(g.item_ids).map(String)),
  ...attnGroups.flatMap((g) => arr(g.item_ids).map(String)),
]);
const leftover = arr(reviews.items).filter((it) => {
  if (bandOf(it) !== "needs_you") return false;
  if (areaOf(it) === EXTERNAL_AREA) return false;
  const id = idOf(it);
  return Boolean(id) && !covered.has(id);
});
const clientFallbackKeys = new Set(leftover.map(fallbackGroupKey));
const externalNeedsYou = arr(reviews.items).filter(
  (it) => areaOf(it) === EXTERNAL_AREA && bandOf(it) === "needs_you",
);
const externalCard = externalNeedsYou.length > 0 ? 1 : 0;

const reviewsRendered = revBackend.length + clientFallbackKeys.size + externalCard;

/* ---- check 3: A reconciles with the Reviews-rendered count via an explicit allowlist ---- */
const nonReviewsSources = Object.entries(bySource).filter(([s]) => s !== "reviews");
const allowlist = [
  ...nonReviewsSources.map(([s, n]) => [
    n,
    `attention "${s}"-source needs-you groups render on their own screen (Agents/Work), not Reviews`,
  ]),
  [-clientFallbackKeys.size, "Reviews client fallback safety-net groups for areas the projection has not grouped yet (writer debt; retires when counts.fallback_groups == 0)"],
  [-externalCard, "external-repo-intake needs-you rows collapse to one Reviews card"],
];
const explainedDelta = allowlist.reduce((sum, [n]) => sum + n, 0);
const observedDelta = A - reviewsRendered;

console.log("attention-consistency-smoke");
console.log(`  attention.counts.needs_you_groups        = ${A}`);
console.log(`  Reviews page rendered needs_you groups   = ${reviewsRendered}`);
console.log(`    reviews backend groups   ${revBackend.length}`);
console.log(`    client fallback groups   ${clientFallbackKeys.size}`);
console.log(`    external card            ${externalCard}`);
console.log(`  needs_you groups by source: ${JSON.stringify(bySource)}`);
console.log("  delta allowlist (attention groups minus what Reviews renders):");
for (const [n, reason] of allowlist) console.log(`    ${n >= 0 ? "+" : ""}${n}  ${reason}`);

assert.equal(
  observedDelta,
  explainedDelta,
  `attention.needs_you_groups (${A}) vs Reviews-rendered (${reviewsRendered}) delta ${observedDelta} is not fully explained by the allowlist (${explainedDelta}) — an un-accounted "needs me" heuristic has appeared`,
);

/* ---- check 4: every non-copy-only action is a clean argv[0]==="compound" ---- */
let checkedActions = 0;
for (const g of attnGroups) {
  const action = g.action;
  if (!action || typeof action !== "object") continue;
  if (str(action.kind) === "copy_only") continue;
  checkedActions++;
  const argv = arr(action.argv).map(String);
  assert.ok(
    argv.length >= 2 && argv[0] === "compound",
    `group ${str(g.key)} action ${str(action.id)} (kind=${str(action.kind)}) must carry a clean argv starting with "compound"; got ${JSON.stringify(argv)}`,
  );
}

console.log(
  `  action argv check: ${checkedActions} non-copy-only action(s) all clean (argv[0]="compound")`,
);
console.log("attention-consistency-smoke ok");
