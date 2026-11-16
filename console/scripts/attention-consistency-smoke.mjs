import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";

// proof-check invokes console smokes with Node. The real public seam below is
// TSX, so re-enter this same script through the repo's Bun runtime before any
// TSX module is loaded. Static imports would fail in Node before this guard ran.
if (!process.versions.bun) {
  execFileSync("bun", [fileURLToPath(import.meta.url)], {
    cwd: process.cwd(),
    env: process.env,
    stdio: "inherit",
  });
  process.exit(0);
}

// Standalone renders model an unpaired, read-only browser. Never inherit a
// supervisor's session posture or ask Next for cookies outside a request.
process.env.COMPOUND_CONSOLE_OPERATOR_SESSION = "0";

const [
  { default: ReviewsPage },
  { getAttentionProjection, housekeepingGroups, toAttentionReport, yourDecisionGroups },
  { NeedsYouList },
  { decisionSelectionHref, initialDecideIndex, initialDecideKey },
  { enrichAttentionGroups },
  { buildDecideGroups },
  { decisionKindWords },
] = await Promise.all([
  import("../src/app/reviews/page.tsx"),
  import("../src/lib/attention.ts"),
  import("../src/components/briefing/NeedsYouList.tsx"),
  import("../src/components/decide/DecideQueue.tsx"),
  import("../src/lib/review-data.ts"),
  import("../src/lib/decide-model.ts"),
  import("../src/lib/decision-registry.ts"),
]);

/*
 * Acceptance: ONE attention projection owns every "what needs you" identity.
 * This smoke executes the real Reviews server component, then proves that the
 * Decide queues render exactly the needs-you keys emitted by
 * `compound attention --json`.
 *
 * Reviews may enrich an Attention group with item detail, and the page may
 * present every workflow in one queue. It may not add, drop, replace,
 * duplicate, or reorder identities.
 *
 * Run manually / through proof-check:
 *   COMPOUND_BIN=compound COMPOUND_HOME=~/.compound bun scripts/attention-consistency-smoke.mjs
 */

const str = (value) => (typeof value === "string" ? value : "");
const arr = (value) => (Array.isArray(value) ? value : []);
const num = (value) => (typeof value === "number" ? value : 0);

function decideQueuesFromPage(root) {
  const queues = [];
  const visit = (node) => {
    if (Array.isArray(node)) {
      node.forEach(visit);
      return;
    }
    if (!node || typeof node !== "object") return;
    if (node.type?.name === "DecideQueue" && Array.isArray(node.props?.groups)) {
      queues.push(node);
    }
    visit(node.props?.children);
  };
  visit(root);
  return queues;
}

// Seed the production memo once, then render the real page. A separate direct
// child process here would make the smoke pay for the same expensive
// projection twice and turn unrelated machine contention into a false
// identity failure.
const attentionProjection = await getAttentionProjection({ timeoutMs: 180_000 });
assert.notEqual(
  attentionProjection.read.status,
  "unreachable",
  `Attention projection is unreachable: ${attentionProjection.read.error ?? "unknown error"}`,
);
const attention = attentionProjection.report;
assert.ok(attention, "Attention projection returned no report");
assert.equal(str(attention.schema_version), "compound.attention.v0.1", "unexpected attention schema");
assert.ok(attention.operatorAttention, "operator_attention must remain separate from Attention groups");

const attentionGroups = arr(attention.groups).filter((group) => str(group.band) === "needs_you");
const attentionKeys = attentionGroups.map((group) => str(group.key));
assert.ok(attentionKeys.every(Boolean), "every Attention needs-you group must carry a stable key");
assert.equal(
  num(attention.counts?.needs_you_groups),
  attentionKeys.length,
  "counts.needs_you_groups must equal the actual Attention needs-you group count",
);
assert.equal(new Set(attentionKeys).size, attentionKeys.length, "Attention needs-you keys must be unique");

const page = await ReviewsPage({ searchParams: Promise.resolve({}) });
const queues = decideQueuesFromPage(page);
const queueKeys = queues.map((queue) => queue.props.groups.map((group) => str(group.key)));
const decideKeys = queueKeys.flat();

assert.equal(
  queues.length,
  yourDecisionGroups(attention).length > 0 ? 1 : 0,
  "Reviews must render one Attention-ordered Decide queue (or none for a live-empty projection)",
);
assert.equal(new Set(decideKeys).size, decideKeys.length, "a decision key rendered in more than one Decide queue");

// Decide lists only what is his (lib/attention.ts yourDecisionGroups): the
// projection's needs-you groups minus agent housekeeping (skill proposals),
// which fold into one quiet line. Every needs-you key is one or the other.
const yourKeys = yourDecisionGroups(attention).map((group) => str(group.key));
const housekeepingKeys = housekeepingGroups(attention).map((group) => str(group.key));
assert.deepEqual(
  [...yourKeys, ...housekeepingKeys].sort(),
  [...attentionKeys].sort(),
  "every Attention needs-you group is either his (a Decide card) or agent housekeeping (the folded line)",
);
const onlyInAttention = yourKeys.filter((key) => !decideKeys.includes(key));
const onlyInDecide = decideKeys.filter((key) => !yourKeys.includes(key));
assert.deepEqual(
  { onlyInAttention, onlyInDecide },
  { onlyInAttention: [], onlyInDecide: [] },
  "Decide must list exactly the needs-you groups that are his",
);

assert.deepEqual(decideKeys, yourKeys, "Decide must keep the one shared order (Attention's, unworded last)");

const syntheticAttention = toAttentionReport({
  schema_version: "compound.attention.v0.1",
  generated_at: "2026-07-24T12:00:00Z",
  counts: { needs_you: 1, needs_you_groups: 1, actionable_now: 0, watching: 0, blocked_on_you: 1, fallback_groups: 0 },
  top: { key: "decide-one", band: "needs_you" },
  groups: [{ key: "decide-one", band: "needs_you" }],
  operator_attention: {
    schema_version: "compound.operator_attention_projection.v0.1",
    revision: "sha256:attention",
    total: 2,
    items: [
      {
        event_ref: "wco_human",
        work_ref: "work_human",
        delivery_class: "human_attention",
        delta: { kind: "authority", subject_ref: "pol_human", before: "pending", after: "unresolved" },
        artifact_refs: [`artifact:sha256:${"1".repeat(64)}`],
        semantic_revision: `sha256:${"2".repeat(64)}`,
      },
      {
        event_ref: "wco_silent",
        work_ref: "work_silent",
        delivery_class: "silent_state",
        delta: { kind: "scope", subject_ref: "scope:silent", before: "open", after: "reserved" },
        artifact_refs: [`artifact:sha256:${"3".repeat(64)}`],
        semantic_revision: `sha256:${"4".repeat(64)}`,
      },
    ],
  },
});
assert.ok(syntheticAttention, "synthetic Attention report must normalize");
assert.equal(syntheticAttention.counts.needs_you_groups, 1, "operator_attention must not change Decide counts");
assert.deepEqual(syntheticAttention.groups.map((group) => group.key), ["decide-one"], "operator_attention must not duplicate Decide identity");
assert.deepEqual(
  syntheticAttention.operatorAttention.items.map((item) => item.eventRef),
  ["wco_human"],
  "Attention loader must human-filter operator_attention independently",
);

// Today's answer (NeedsYouList) shows at most five decisions in Attention's
// order, each deep-linked to its exact key, then one link to the rest.
// Today lists the decisions he can answer, in Decide's order: its rows are
// Decide's top rows.
const todayGroups = yourDecisionGroups(attention).filter((group) => !group.needs_wording);
assert.deepEqual(
  todayGroups.map((group) => str(group.key)),
  decideKeys.slice(0, todayGroups.length),
  "Today's rows must be Decide's top rows, in the same order",
);
const briefing = NeedsYouList({
  groups: todayGroups,
  total: todayGroups.length,
  act: null,
  mutationsEnabled: false,
});
const briefingHrefs = [];
const collectHrefs = (node) => {
  if (Array.isArray(node)) {
    node.forEach(collectHrefs);
    return;
  }
  if (!node || typeof node !== "object") return;
  // A row may also link the app it is about; the decision links go to Decide,
  // or to the page that owns the choice (proposed memories: the Memory page).
  if (typeof node.props?.href === "string" && /^\/(reviews|memory)\b/.test(node.props.href)) briefingHrefs.push(node.props.href);
  collectHrefs(node.props?.children);
};
collectHrefs(briefing);
assert.deepEqual(
  briefingHrefs,
  [
    ...todayGroups.slice(0, 5).map((group) => decisionKindWords(str(group.area))?.answerAt?.href ?? `/reviews?decision=${encodeURIComponent(str(group.key))}`),
    // "N more" opens the first decision Today does not show, never plain Decide (which reopens row 1).
    ...(todayGroups.length > 5 ? [`/reviews?decision=${encodeURIComponent(str(todayGroups[5].key))}`] : []),
  ],
  "every Today decision row must deep-link its exact Attention key (or the page that owns its choice), in Attention's order",
);

const heroKey = "decision-candidates|fixture hero identity";
briefingHrefs.length = 0;
collectHrefs(NeedsYouList({
  groups: [{
    key: heroKey,
    area: "decision-candidates",
    operator_summary: "A decision needs review.",
    operator_unlock: "",
    terminal_reason: "",
    oldest_created_at: "",
    reversibility: "reversible",
    count: 1,
  }],
  total: 1,
  act: null,
  mutationsEnabled: false,
}));
assert.deepEqual(
  briefingHrefs,
  [`/reviews?decision=${encodeURIComponent(heroKey)}`],
  "Today's first decision must deep-link its exact Attention key",
);

// Attention chooses identity, membership, and order. A matching Reviews group
// may enrich that identity with evidence and the owner's exact batch action;
// dropping either makes a card look empty or routes a panel action through an
// incompatible generic path.
{
  const key = "panel-verdicts|prun_fixture_enrichment|promote";
  const item = { id: "panel_enrichment_member01", area: "panel-verdicts" };
  const attentionGroup = {
    key,
    area: item.area,
    band: "needs_you",
    count: 1,
    item_ids: [item.id],
    operator_summary: "Apply the reviewed panel run.",
  };
  const reviewsGroup = {
    key,
    area: item.area,
    band: "needs_you",
    count: 1,
    item_ids: [item.id],
    evidence_refs: ["panel-run:prun_fixture_enrichment"],
    evidence_summaries: [{ ref: "panel-run:prun_fixture_enrichment", summary: "reviewed run" }],
    sample: item,
    safe_batch_action: {
      id: "panel_run_apply",
      label: "Apply reviewed run",
      argv: ["compound", "panel", "run", "--plan", "prun_fixture_enrichment", "--apply"],
      requires_review: true,
    },
  };
  const enriched = enrichAttentionGroups([attentionGroup], [reviewsGroup]);
  assert.equal(enriched.length, 1, "Reviews enrichment must not add an identity");
  assert.deepEqual(enriched[0].item_ids, [item.id], "Attention must retain exact group membership");
  assert.deepEqual(enriched[0].evidence_refs, reviewsGroup.evidence_refs, "matching Reviews evidence must survive");
  const decided = buildDecideGroups(enriched, new Map([[item.id, item]]), { groups: [attentionGroup] }, () => item)[0];
  assert.equal(decided.act.mode, "batch-atomic", "matching panel safe_batch_action must survive enrichment");
  assert.equal(decided.capability.kind, "apply", "panel apply capability must remain executable");
  assert.equal(decided.evidence.length, 1, "matching Reviews evidence must reach the Decide card");

  const mismatched = enrichAttentionGroups(
    [attentionGroup],
    [{ ...reviewsGroup, count: 2, item_ids: [item.id, "review_only_member"] }],
  )[0];
  assert.deepEqual(mismatched.item_ids, [item.id], "Attention must retain exact group membership");
  assert.deepEqual(
    mismatched.review_item_ids,
    [item.id, "review_only_member"],
    "mismatched Reviews membership must remain available under an explicitly read-only enrichment key",
  );
  assert.equal(
    mismatched.safe_batch_action,
    undefined,
    "a Reviews batch action must not cross a mismatched member set",
  );
}

if (yourKeys.length > 0) {
  const requestedKey = yourKeys.at(-1);
  const selectedPage = await ReviewsPage({ searchParams: Promise.resolve({ decision: requestedKey }) });
  const selectedQueues = decideQueuesFromPage(selectedPage);
  assert.equal(
    selectedQueues.length,
    1,
    "a deep-linked decision must render in the one Attention-ordered queue without a competing fallback queue",
  );
  const containingQueue = selectedQueues[0];
  assert.deepEqual(
    containingQueue.props.groups.map((group) => str(group.key)),
    yourKeys,
    "deep-link rendering must preserve the complete Attention order",
  );
  assert.equal(
    containingQueue.props.initialSelectedKey,
    requestedKey,
    "the Decide queue containing a deep-linked decision must receive that exact initialSelectedKey",
  );
  assert.equal(
    initialDecideKey(containingQueue.props.groups, requestedKey),
    requestedKey,
    "DecideQueue must initialize to the requested key in the Attention-ordered queue",
  );

  assert.equal(
    initialDecideIndex(containingQueue.props.groups, requestedKey),
    yourKeys.length - 1,
    "the deep-linked queue must initialize its keyboard index to the requested decision",
  );

  const selectionHref = decisionSelectionHref(
    "/reviews",
    "view=compact&decision=old-key&source=briefing",
    requestedKey,
  );
  const selectionURL = new URL(selectionHref, "https://console.invalid");
  assert.equal(selectionURL.pathname, "/reviews", "selection sync must stay on the current route");
  assert.equal(selectionURL.searchParams.get("decision"), requestedKey, "selection sync must write the exact decision key");
  assert.equal(selectionURL.searchParams.get("view"), "compact", "selection sync must preserve unrelated query params");
  assert.equal(selectionURL.searchParams.get("source"), "briefing", "selection sync must preserve query provenance");
  assert.equal(selectionURL.searchParams.getAll("decision").length, 1, "selection sync must replace, not duplicate, the decision param");
}

const reviewsPageURL = new URL("../src/app/reviews/page.tsx", import.meta.url).href;
const compoundURL = new URL("../src/lib/compound.ts", import.meta.url).href;
// Decide never waits for compound: the first render says attention is being
// read; once the (failing) read lands, it says the read failed. Neither may
// render an empty or stale Decide queue.
const degradationProbe = `
  import ReviewsPage from ${JSON.stringify(reviewsPageURL)};
  import { settleProjectionReads } from ${JSON.stringify(compoundURL)};
  const scan = (root) => {
    const seen = { loading: false, failed: false, queues: 0 };
    const visit = (node) => {
      if (Array.isArray(node)) { node.forEach(visit); return; }
      if (!node || typeof node !== "object") return;
      // The page answers its question in QuestionHeader's \`answer\` slot, so the
      // read notice may sit there instead of among the children.
      if (node.type?.name === "ReadNotice" && node.props?.label === "what waits for you") {
        if (node.props.read?.status === "loading") seen.loading = true;
        if (node.props.read?.status === "unreachable") seen.failed = true;
      }
      if (node.type?.name === "DecideQueue") seen.queues += 1;
      visit(node.props?.answer);
      visit(node.props?.children);
    };
    visit(root);
    return seen;
  };
  const first = scan(await ReviewsPage({ searchParams: Promise.resolve({}) }));
  if (!first.loading) throw new Error("a cold Attention read did not render as being read");
  if (first.queues !== 0) throw new Error("a cold Attention read rendered a Decide queue");
  await settleProjectionReads();
  const after = scan(await ReviewsPage({ searchParams: Promise.resolve({}) }));
  if (!after.failed) throw new Error("unreachable Attention rendered as an empty Decide queue");
  if (after.queues !== 0) throw new Error("unreachable Attention rendered a stale Decide queue");
`;
execFileSync("bun", ["-e", degradationProbe], {
  cwd: fileURLToPath(new URL("../..", import.meta.url)),
  env: { ...process.env, COMPOUND_BIN: process.execPath },
  stdio: "pipe",
});

let checkedActions = 0;
for (const group of attentionGroups) {
  const action = group.action;
  if (!action || typeof action !== "object") continue;
  if (str(action.kind) === "copy_only") continue;
  checkedActions++;
  const argv = arr(action.argv).map(String);
  assert.ok(
    argv.length >= 2 && argv[0] === "compound",
    `group ${str(group.key)} action ${str(action.id)} (kind=${str(action.kind)}) must carry a clean argv starting with "compound"; got ${JSON.stringify(argv)}`,
  );
}

console.log("attention-consistency-smoke");
console.log(`  Attention needs-you keys = ${attentionKeys.length}`);
console.log(`  Decide needs-you keys    = ${decideKeys.length}`);
queueKeys.forEach((keys, index) => console.log(`    queue ${index + 1}: ${keys.length}`));
console.log(`  action argv check: ${checkedActions} non-copy-only action(s) all clean (argv[0]="compound")`);
console.log("attention-consistency-smoke ok");
