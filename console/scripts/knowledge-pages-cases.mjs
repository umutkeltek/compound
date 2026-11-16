// Knowledge pages cases: render the real Memory, Rules and Sources pages
// against a synthetic compound binary and check what the person reads.
//
// Memory: no choice control that cannot work (a plain sentence on who
// records the choice, exact commands only behind "For agents"); each card
// says what it is about and where it came from; one name for a proposed
// rule; plain words for each choice; notes from one source fold into one
// group; each card is a link target.
//
// The read layer keeps global state (cache, queue, binary path), so this
// runs in its own process. Imported by the wired work-legibility smoke, it
// re-runs itself as a child; directly: bun scripts/knowledge-pages-cases.mjs
import { fileURLToPath } from "node:url";

if (!import.meta.main) {
  const { spawnSync } = await import("node:child_process");
  const run = spawnSync(process.execPath, [fileURLToPath(import.meta.url)], { stdio: "inherit", env: process.env, timeout: 240_000 });
  if (run.status !== 0) {
    console.error(`knowledge-pages cases FAILED (exit ${run.status ?? run.signal})`);
    process.exit(1);
  }
} else {
  await runCases();
}

async function runCases() {
  const assert = (await import("node:assert/strict")).default;
  const { chmod, mkdtemp, rm, writeFile } = await import("node:fs/promises");
  const { tmpdir } = await import("node:os");
  const { join } = await import("node:path");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { AppRouterContext } = await import("next/dist/shared/lib/app-router-context.shared-runtime.js");
  const { PathnameContext, SearchParamsContext } = await import("next/dist/shared/lib/hooks-client-context.shared-runtime.js");

  const scratch = await mkdtemp(join(tmpdir(), "compound-knowledge-pages-"));
  const bin = join(scratch, "compound-stub");
  // Synthetic home. Memory: two proposed rules, three proposed memories taken
  // from one paper, one about an app, one reading-material row (source
  // evidence, not a choice). Every name, id and host is invented.
  await writeFile(bin, `#!/usr/bin/env bun
const args = process.argv.slice(2).join(" ");
const now = Date.now();
const iso = (daysAgo) => new Date(now - daysAgo * 86400000).toISOString();
if (args === "version") { console.log("synthetic"); process.exit(0); }
if (process.env.KNOWLEDGE_CASES_FAIL && args.startsWith(process.env.KNOWLEDGE_CASES_FAIL)) { console.error("synthetic read failure"); process.exit(7); }
const paper = { ref: "ev_fxpaper01", source_kind: "note", source_id: "knowledge_page:fixture-paper", quote: "# Fixture Paper (example study) — layered helpers for synthetic agents\\n\\nBody text.", observed_at: iso(26) };
const note = (id, object, extra = {}) => ({ id, state: "proposed", entity: "fixture-harness", predicate: "finding", object, confidence: 0.9, derivation: "extracted",
  claim_type: "research", visibility: "contextual", promotion_basis: "curated_page", qualifiers: { source: "knowledge_page", page_id: "fixture-paper" },
  source_trace_ref: "ev_fxpaper01", evidence_refs: [id, "ev_fxpaper01"], evidence: paper, created_at: iso(26), ...extra });
const reply = {
  "memory candidates --json --state proposed --include-evidence": { generated_at: iso(0), counts: { total: 48, matched: 6, returned: 6, by_state: { proposed: 6, parked: 39, no_promote: 3 } }, items: [
    note("cand_fxnote01", "Layered helpers keep each agent's tools small."),
    note("cand_fxnote02", "A helper that fails twice is retired."),
    note("cand_fxnote03", "Helpers share one memory budget."),
    { id: "cand_fxapp01", state: "proposed", entity: "product:fixture-app", predicate: "status", object: "Fixture App ships weekly builds.", confidence: 0.8, derivation: "deterministic",
      qualifiers: { source: "knowledge_page", page_id: "fixture-app-status" }, evidence_refs: ["cand_fxapp01", "ev_fxapp01"], source_trace_ref: "ev_fxapp01",
      evidence: { ref: "ev_fxapp01", quote: "# Fixture App release notes\\nweekly", source_id: "knowledge_page:fixture-app-status" }, created_at: iso(3) },
    { id: "cand_fxread01", state: "proposed", entity: "knowledge:fixture-reading", predicate: "source", object: "A reading-material row", qualifiers: { source: "knowledge_import" }, created_at: iso(5) },
  ] },
  "memory candidates --json --state parked --kind decision": { generated_at: iso(0), counts: { by_state: { proposed: 6, parked: 39, no_promote: 3 } }, items: [
    { id: "cand_fxrule01", state: "parked", entity: "product:fixture-app", predicate: "decided", claim_type: "decision", object: "Fixture App release notes are reviewed before each build.",
      promotion_basis: "explicit_confirmation", qualifiers: { source: "knowledge_page", kind: "ruling", lifecycle_status: "accepted", decision_id: "DEC-FX01" }, created_at: iso(110), recurrence_count: 1 },
    { id: "cand_fxrule02", state: "parked", entity: "decision:fx-ledger-02", predicate: "decided", claim_type: "decision", object: "Fixture backups run nightly.",
      qualifiers: { source: "brain-ledger" }, created_at: iso(140) },
  ] },
  "memory canon --json": { schema_version: "compound.memory.canon.v0.1", distribution: { decision: 4, principle: 1, status: 9, unclassified: 0 }, total: 14 },
};
const value = reply[args];
if (value) { console.log(JSON.stringify(value)); process.exit(0); }
console.log(JSON.stringify({ generated_at: iso(0) }));
`);
  await chmod(bin, 0o755);
  process.env.COMPOUND_BIN = bin;
  process.env.COMPOUND_HOME = join(scratch, "home");
  process.env.COMPOUND_CONSOLE_MUTATIONS = "0";
  process.env.COMPOUND_CONSOLE_OPERATOR_SESSION = "0";
  process.env.COMPOUND_CONSOLE_CACHE_DIR = join(scratch, "cache");
  process.env.TMPDIR = scratch;

  const compound = await import("../src/lib/compound.ts");
  const { BANNED_PHRASES, findBannedPhrases, MEMORY_CHOICE_HANDOFF } = await import("../src/lib/plain-words.ts");
  const { default: Memory } = await import("../src/app/memory/page.tsx");

  const router = Object.fromEntries(["push", "replace", "refresh", "back", "forward", "prefetch"].map((k) => [k, () => {}]));
  const wrap = (path, element) =>
    renderToStaticMarkup(
      createElement(AppRouterContext.Provider, { value: router },
        createElement(PathnameContext.Provider, { value: path },
          createElement(SearchParamsContext.Provider, { value: new URLSearchParams() }, element))));
  // Pages never wait for compound: render, let the queued reads land, render again.
  const settled = async (render) => { await render(); await compound.settleProjectionReads(); return render(); };
  const renderMemory = async () => wrap("/memory", await Memory({ searchParams: Promise.resolve({}) }));
  // What the person reads: tags out, the closed "For agents" disclosures out.
  const forAgents = /<details[^>]*>\s*<summary[^>]*>For agents[\s\S]*?<\/details>/g;
  const visible = (html) => html.replace(forAgents, " ").replace(/<[^>]+>/g, " ").replace(/&#x27;/g, "'").replace(/&quot;/g, '"').replace(/&amp;/g, "&").replace(/\s+/g, " ");
  // Knowledge words that must not reach the person (on top of the shared list).
  const KNOWLEDGE_JARGON = [/ambient/i, /re-?litigat/i, /\borient\b/i, /adjudicat/i, /standing law/i, /\bcandidate/i, /\brecall\b/i, /\bpromot/i, /\bparked\b/i];

  try {
    /* ---------- Memory ---------- */
    const memory = await settled(renderMemory);
    const text = visible(memory);
    for (const phrase of findBannedPhrases(text)) assert.fail(`Memory shows a banned phrase: ${phrase}`);
    for (const re of KNOWLEDGE_JARGON) assert.doesNotMatch(text, re, `Memory shows system jargon ${re}`);

    // KNOW-01: no control that cannot work; who records the choice, in one sentence.
    assert.doesNotMatch(memory, /<button[^>]*\sdisabled=""/, "no disabled button stands in for an action");
    assert.doesNotMatch(text, /Record ·|Check console access|Launch via/, "no dead Record button or wrong fix");
    assert.doesNotMatch(memory, /href="\/settings"/, "no link to a page that cannot fix it");
    assert.equal((text.match(new RegExp(MEMORY_CHOICE_HANDOFF.replace(/[.;]/g, "\\$&"), "g")) ?? []).length, 6, "each of the six cards says who records the choice");
    assert.match(memory, /compound memory adjudicate cand_fxrule01 --verdict promote/, "the exact command, with the real id, is there for agents");
    assert.match(memory, /compound memory adjudicate cand_fxnote01 --verdict promote --basis curated_page/);
    assert.doesNotMatch(memory, /&lt;id&gt;|<id>/, "no command with a blank");

    // KNOW-05: what it is about, where it came from, one age, no raw chips.
    assert.match(text, /About: fixture harness/);
    assert.match(memory, /href="\/apps\/fixture-app"[^>]*>Fixture App</, "an app subject names and links the app");
    assert.match(text, /From: Fixture Paper \(example study\) — layered helpers for synthetic agents/, "the source's own heading");
    assert.match(memory, /href="\/map\/lineage\/ev_fxpaper01"/, "the source links to its evidence history");
    assert.doesNotMatch(text, /No source reference was returned|age unknown|conf 0\.9|basis: curated_page|Recorded page/);
    assert.match(text, /added 26d ago/);

    // KNOW-07 (words) and KNOW-19: one name, plain choices.
    assert.match(text, /Proposed rule /);
    assert.doesNotMatch(text, /Proposed standing rule|Standing law · candidate/i);
    assert.match(text, /Make it a rule Every agent will be told this at the start of each task\./);
    assert.match(text, /Not now Keep it on file; agents won't be told\./);
    assert.match(text, /Accept Future agents will be told this when it is relevant\./);
    assert.match(text, /Stop asking Don't ask me about this source again\./);
    assert.match(text, /Waiting for you/);
    assert.match(text, /2 proposed rules and 4 proposed memories wait for your choice/);

    // KNOW-16: every card is a link target.
    for (const id of ["cand_fxrule01", "cand_fxrule02", "cand_fxnote01", "cand_fxnote02", "cand_fxnote03", "cand_fxapp01"]) {
      assert.match(memory, new RegExp(`<article id="${id}"`), `${id} is a link target`);
    }

    console.log("knowledge-pages cases ok: Memory");
  } finally {
    await compound.settleProjectionReads();
    await rm(scratch, { recursive: true, force: true });
  }
}
