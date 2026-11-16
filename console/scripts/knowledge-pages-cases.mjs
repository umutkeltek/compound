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
  // Rules: seven rows, six decisions. DEC-FX01 is listed twice (once as the
  // decision record, its id in the claim slot; once as the accepted claim).
  // Two distinct decisions share the title "Promotion".
  "decisions standing-law --json": { schema_version: "compound.standing_law_view.v0.1", generated_at: iso(0), conflicts: [],
    counts: { records: 7, rulings: 1, grants: 1, freezes: 0, superseded: 0, conflicts: 0 },
    records: [
      { claim_id: "claim_fxpolicya", decision_id: "DEC-FX01", kind: "policy", title: "Fixture policy A keeps builds reproducible", date: iso(140), corroboration_count: 0 },
      { claim_id: "DEC-FX01", decision_id: "", kind: "policy", title: "Fixture policy A keeps builds reproducible", scope: "cross-agent", date: iso(141), corroboration_count: 0 },
      { claim_id: "claim_fxpromo2", decision_id: "DEC-FX02", kind: "policy", title: "Promotion", date: iso(120), corroboration_count: 0 },
      { claim_id: "claim_fxpromo3", decision_id: "DEC-FX03", kind: "policy", title: "Promotion", date: iso(119), corroboration_count: 0 },
      { claim_id: "claim_fxruling", decision_id: "fx-ruling-01", kind: "ruling", title: "Fixture disks keep 150 GiB free", reviewed_by: "operator:example-via-claude_code",
        source_path: "/home/example/.compound/decisions/operator/fx-ruling-01.md", falsifier: "A later decision replaces this one.", review_trigger: "Free space drops twice in a week.", date: iso(20), corroboration_count: 0 },
      { claim_id: "claim_fxops", decision_id: "", kind: "operational", title: "Fixture weekly sync runs on Mondays", reviewed_by: "", source_path: "knowledge_page", date: iso(30), corroboration_count: 0 },
      { claim_id: "claim_fxgrant", decision_id: "fx-grant-01", kind: "grant", title: "Agents may restart the fixture job on their own", reviewed_by: "operator", date: iso(10),
        corroboration_count: 2, corroboration_evidence: ["ev_fxcorr1"] },
    ] },
};
// Sources: six connections. The CLI's own labels contradict the dates on
// purpose (an "observed" feed 27 days old, a "stale" one 2 days old, an
// import 137 days old) and its counts are wrong on purpose: the page must
// derive one status per source from the date it shows.
const ago = (hours) => new Date(now - hours * 3600000).toISOString();
reply["source connections --json"] = { schema_version: "compound.source_connections_projection.v0.2", generated_at: iso(0),
  counts: { total: 99, evidence_observed: 0, action_required: 42 },
  connections: [
    { id: "fx_feed_old", label: "Fixture daily feed", evidence_state: "observed", availability: "needs_private_config", private_setup_needed: true, last_seen: ago(27 * 24), row_count: 2900,
      produces: ["product signal", "product loop evidence"], source_kinds: ["fixture_feed"], next_safe_action: "Refresh stale source observation", latest_cards: [] },
    { id: "fx_feed_recent", label: "Fixture search feed", evidence_state: "stale", availability: "unknown", last_seen: ago(48), row_count: 120, produces: ["search signal"], source_ref: "site:https://fixture.example",
      next_safe_action: "Refresh stale source observation",
      latest_cards: [{ id: "sicard_fx0001", kind: "stale_source", title: "Refresh stale source observation", command: ["compound", "product", "loops", "--product", "product:fixture-app", "--json"] }] },
    { id: "fx_import_old", label: "Fixture papers", evidence_state: "imported", availability: "unknown", last_seen: ago(137 * 24), row_count: 5, produces: ["knowledge evidence", "memory candidate"], latest_cards: [] },
    { id: "fx_never", label: "Fixture stars", evidence_state: "never_seen", availability: "not_configured", row_count: 0, produces: ["session evidence"], latest_cards: [] },
    { id: "fx_setup", label: "Fixture ads", evidence_state: "never_seen", availability: "needs_private_config", private_setup_needed: true, row_count: 0, latest_cards: [] },
    { id: "fx_ads_recent", label: "Fixture ad spend", evidence_state: "observed", availability: "unknown", last_seen: ago(2), row_count: 358, produces: ["paid acquisition rows", "product signal"], latest_cards: [] },
  ] };
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
  const { default: Rules } = await import("../src/app/standing-law/page.tsx");
  const { default: Sources } = await import("../src/app/sources/page.tsx");

  const router = Object.fromEntries(["push", "replace", "refresh", "back", "forward", "prefetch"].map((k) => [k, () => {}]));
  const wrap = (path, element) =>
    renderToStaticMarkup(
      createElement(AppRouterContext.Provider, { value: router },
        createElement(PathnameContext.Provider, { value: path },
          createElement(SearchParamsContext.Provider, { value: new URLSearchParams() }, element))));
  // Pages never wait for compound: render, let the queued reads land, render again.
  const settled = async (render) => { await render(); await compound.settleProjectionReads(); return render(); };
  const renderMemory = async () => wrap("/memory", await Memory({ searchParams: Promise.resolve({}) }));
  const renderRules = async () => wrap("/standing-law", await Rules());
  const renderSources = async (status) => wrap("/sources", await Sources({ searchParams: Promise.resolve(status ? { status } : {}) }));
  const card = (html, id) => (html.match(new RegExp(`<article id="source-${id}"[\\s\\S]*?</article>`)) ?? [""])[0];
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

    // KNOW-09: three notes from one paper are one group; the app note stands alone.
    assert.equal((memory.match(/data-memory-group="3"/g) ?? []).length, 1, "notes from one source fold into one group");
    assert.match(text, /3 proposed memories from one source About fixture harness, from Fixture Paper/);
    assert.doesNotMatch(memory, /data-memory-group="1"/);

    // KNOW-16: every card is a link target.
    for (const id of ["cand_fxrule01", "cand_fxrule02", "cand_fxnote01", "cand_fxnote02", "cand_fxnote03", "cand_fxapp01"]) {
      assert.match(memory, new RegExp(`<article id="${id}"`), `${id} is a link target`);
    }

    console.log("knowledge-pages cases ok: Memory");

    /* ---------- Rules ---------- */
    // KNOW-18: the proposed-rule count is unknown while its read loads or fails.
    compound.invalidateRunJsonCache();
    process.env.KNOWLEDGE_CASES_FAIL = "memory candidates --json --state parked";
    const rulesCold = visible(await renderRules());
    assert.match(rulesCold, /Proposed rules waiting for you .*— waiting/, "a loading count is —, not 0");
    assert.match(rulesCold, /Reading proposed rules for the first time/);
    assert.doesNotMatch(rulesCold, /\b0 waiting|No proposed rules wait for you/, "a loading read never says nothing waits");
    await compound.settleProjectionReads();
    const rulesFailed = visible(await renderRules());
    assert.match(rulesFailed, /— waiting/);
    assert.match(rulesFailed, /Could not read proposed rules/);
    assert.doesNotMatch(rulesFailed, /\b0 waiting|No proposed rules wait for you/, "a failed read never says nothing waits");
    delete process.env.KNOWLEDGE_CASES_FAIL;
    compound.invalidateRunJsonCache();

    const rules = await settled(renderRules);
    const rulesText = visible(rules);
    for (const phrase of findBannedPhrases(rulesText)) assert.fail(`Rules shows a banned phrase: ${phrase}`);
    for (const re of [/standing law/i, /adjudicat/i, /\bcandidate/i, /\bparked\b/i, /falsifier/i, /provenance/i, /corroboration/i]) {
      assert.doesNotMatch(rulesText, re, `Rules shows system jargon ${re}`);
    }

    // KNOW-10: each decision once; kind chips add up to the total; no zero restatements.
    assert.match(rulesText, /6 rules in force/, "the duplicated decision counts once");
    assert.doesNotMatch(rulesText, /7 (rules )?in force|\d+R · \d+G · \d+F/);
    assert.match(rulesText, /6 all 3 policies 1 permission 1 operating note 1 your ruling/, "kind chips add up to the total");
    assert.match(rulesText, /1 decision the source listed twice is shown once/);
    assert.equal((rulesText.match(/Fixture policy A keeps builds reproducible/g) ?? []).length, 1, "the duplicate row is folded");
    assert.equal((rulesText.match(/\bPromotion\b/g) ?? []).length, 2, "distinct decisions with one title stay distinct");
    assert.match(rulesText, /applies to: cross-agent/, "the folded row keeps the other row's scope");
    assert.match(rulesText, /restated 2 times/);
    assert.doesNotMatch(rulesText, /0 corroborations|restated 0/);

    // KNOW-11: where a rule came from, how it ends, how to change it; raw paths only for agents.
    assert.match(rulesText, /This rule ends when A later decision replaces this one\./);
    assert.match(rulesText, /Your ruling, recorded by you through Claude Code on \d{4}-\d{2}-\d{2}\./);
    assert.match(rulesText, /Permission, recorded by you on/);
    assert.match(rules, /href="\/map\/lineage\/claim_fxruling"[^>]*>Read its history/);
    assert.match(rules, /href="\/map\/lineage\/ev_fxcorr1"/, "a restatement links to its evidence history");
    assert.match(rulesText, /To change or retire a rule, ask an agent to record a new decision that replaces it/);
    assert.doesNotMatch(rulesText, /\/home\/example|reviewed by operator|knowledge_page claim_/, "raw paths and ids stay behind For agents");
    assert.match(rules, /source: \/home\/example\/\.compound\/decisions\/operator\/fx-ruling-01\.md/, "the raw path is still there for agents");

    // KNOW-16 and KNOW-07 words: links land on the item; one name for a proposed rule.
    assert.match(rules, /href="\/memory#memory-review"[^>]*>2 proposed rules wait for your choice on the Memory page/);
    assert.match(rules, /href="\/memory#cand_fxrule01"[^>]*>Decide on the Memory page/);
    assert.match(rulesText, /Proposed rule Fixture App release notes are reviewed before each build\./);
    assert.match(rulesText, /Waiting for you/);
    assert.match(rulesText, /2 waiting/);
    assert.doesNotMatch(rulesText, /Adjudicate on|\/memory/);

    console.log("knowledge-pages cases ok: Rules");

    /* ---------- Sources ---------- */
    // KNOW-17: a read not landed is "reading", a failed read says so; never zeros.
    compound.invalidateRunJsonCache();
    process.env.KNOWLEDGE_CASES_FAIL = "source connections";
    const sourcesCold = await renderSources();
    assert.match(visible(sourcesCold), /Reading the source connections for the first time/);
    assert.match(visible(sourcesCold), /— sources in all/, "unknown tiles show —");
    assert.doesNotMatch(visible(sourcesCold), /\b0 sources in all|\b0 out of date|live ·/);
    await compound.settleProjectionReads();
    const sourcesFailed = visible(await renderSources());
    assert.match(sourcesFailed, /Could not read the source connections/);
    assert.match(sourcesFailed, /— sources in all/);
    assert.doesNotMatch(sourcesFailed, /\b0 sources in all|No sources are listed|live ·/, "a failed read never shows zeros or an empty list");
    delete process.env.KNOWLEDGE_CASES_FAIL;
    compound.invalidateRunJsonCache();

    const sources = await settled(() => renderSources());
    const sourcesText = visible(sources);
    for (const phrase of findBannedPhrases(sourcesText)) assert.fail(`Sources shows a banned phrase: ${phrase}`);
    // KNOW-15: one status per source that agrees with its date; tiles add up.
    assert.match(visible(card(sources, "fx_feed_old")), /Out of date .*No new data since 27 days ago/, "a 27-day-old feed is out of date, whatever the CLI label");
    assert.match(visible(card(sources, "fx_feed_recent")), /Up to date .*Last data 2 days ago/, "a 2-day-old feed is up to date");
    assert.match(visible(card(sources, "fx_import_old")), /Imported .*Last import 137 days ago/);
    assert.doesNotMatch(card(sources, "fx_import_old"), /text-good/, "an old import is never shown green");
    assert.match(visible(card(sources, "fx_never")), /Not connected/);
    assert.match(visible(card(sources, "fx_setup")), /Needs setup/);
    assert.doesNotMatch(sourcesText, /availability|unknown ·|observed|stale\b/i, "no raw CLI labels and no unknown availability");
    assert.match(sourcesText, /6 sources in all 1 out of date 1 need setup 0 age unknown 2 up to date 1 imported 1 not connected/, "tiles name what they count and add up");
    assert.doesNotMatch(sourcesText, /\b99\b|\b42\b/, "the page never shows the CLI's own (wrong) totals");
    // Tiles filter, and the filter is in the address.
    assert.match(sources, /href="\/sources\?status=out_of_date#source-connections"/);
    const outOfDate = await renderSources("out_of_date");
    assert.ok(card(outOfDate, "fx_feed_old") && !card(outOfDate, "fx_feed_recent"), "the out-of-date filter shows only out-of-date sources");
    // KNOW-12: no dead button; one real next step; commands only for agents, labelled.
    assert.doesNotMatch(sources, /<button[^>]*\sdisabled=""/);
    assert.doesNotMatch(sourcesText, /Plan intake work|Check connection settings/);
    assert.doesNotMatch(sources, /href="\/settings/, "no link to a page that cannot fix it");
    assert.match(visible(card(sources, "fx_feed_old")), /Nothing refreshes it yet: it needs a private setting on this computer, which an agent adds with you\./);
    assert.match(card(sources, "fx_feed_recent"), /href="\/apps\/fixture-app"[^>]*>See this app&#x27;s numbers/, "a source that names an app links to it");
    assert.match(card(sources, "fx_ads_recent"), /href="\/apps"[^>]*>See app numbers/);
    assert.match(sources, /For agents: look at this source \(these commands do not refresh it\)/);
    assert.match(sources, /compound product loops --product product:fixture-app --json/, "the read-only command is still there for agents");
    assert.doesNotMatch(sourcesText, /compound /, "no command in the text he reads");
    // KNOW-21: kit tokens only, so dark mode applies.
    assert.doesNotMatch(sources, /bg-(emerald|slate|amber|red|violet|blue|pink)-\d{2,3}|text-(emerald|slate|amber)-\d{3}/, "no fixed light-only colours");

    console.log("knowledge-pages cases ok: Sources");
  } finally {
    await compound.settleProjectionReads();
    await rm(scratch, { recursive: true, force: true });
  }
}
