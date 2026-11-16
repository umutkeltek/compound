// Decide and Today, round 2: one inbox that explains itself.
// Pure falsifiers for the waiting list's newer shape (new kinds with their
// items, set-aside reasons in groups that add up, what a standing rule
// covers, whether the rules were read, every recorded answer) and for the
// lines under Today's list. No home, binary or cache: everything here is a
// pure function or a component rendered to a string, so it runs in-process.
// Run: bun scripts/decide-today-cases.mjs. The wired gate runs it:
// work-legibility-smoke.mjs imports it, and a failing case fails that smoke.
import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";

const {
  attentionShare, coveredItemIds, housekeepingGroups, productVerdict, toAttentionReport, waitingOnYou, yourDecisionGroups,
} = await import("../src/lib/attention.ts");
const { describeWaiting, setAsideGroups } = await import("../src/lib/counts.ts");
const { answeredRow, cleanTitle, decisionCard, decisionHeadline, recommendationWords, waitingAge } = await import("../src/lib/decide-model.ts");
const { decisionCounter } = await import("../src/components/decide/DecideQueue.tsx");
const { NeedsYouList } = await import("../src/components/briefing/NeedsYouList.tsx");
const { WaitingNotes } = await import("../src/components/decide/WaitingNotes.tsx");
const { BriefingHeader } = await import("../src/components/briefing/BriefingHeader.tsx");
const { decideAnsweredHref, decideItemHref, decideSetAsideHref } = await import("../src/lib/links.ts");
const { memoryChoice, plainArea } = await import("../src/lib/plain-words.ts");
const { memoryReviewHref } = await import("../src/lib/links.ts");

const NOW = Date.parse("2031-03-10T12:00:00Z");
const daysAgo = (n) => new Date(NOW - n * 86_400_000).toISOString();
const text = (html) => html.replace(/<[^>]+>/g, " ").replace(/&quot;/g, '"').replace(/&#x27;|&#39;/g, "'").replace(/&amp;/g, "&").replace(/\s+/g, " ");

const group = (key, area, extra = {}) => ({
  key, area, band: "needs_you", source: "reviews", kind: "adjudicate", count: 1, operator_summary: `A ${area} record.`,
  operator_unlock: "", blast_radius: "local_reversible", reversibility: "reversible", leverage: 1, oldest_created_at: daysAgo(3),
  action: null, terminal_reason: "", item_ids: [`${key}-item`], freshness: { generated_at: daysAgo(0), source: "fixture" }, ...extra,
});

const raw = {
  schema_version: "compound.attention.v0.1",
  generated_at: daysAgo(0),
  counts: { needs_you: 0, needs_you_groups: 0 },
  groups: [
    group("work:held", "workbench-blocked-on-you", { source: "workbench", kind: "gate_admit", count: 2, item_ids: ["work_fx_a", "work_fx_b"] }),
    group("memory:memory-proposals", "memory-proposals", {
      source: "memory", kind: "memory_proposal", count: 3, item_ids: ["cand_fx1", "cand_fx2", "cand_fx3"],
      operator_summary: "3 proposed memories wait for your yes or no.",
      items: ["cand_fx1", "cand_fx2", "cand_fx3"].map((id) => ({ id, title: `Fixture claim ${id}`, context: "fixture-portfolio", ref: `compound://memory/candidates/${id}`, path: "", created_at: daysAgo(4) })),
    }),
    group("product:product-results-due", "product-results-due", {
      source: "product", kind: "product_result", item_ids: ["hyp_fx_due"],
      operator_summary: "1 product change has a result ready for your verdict: did it work?",
      items: [{ id: "hyp_fx_due", title: "fixture_signups moves in response to the new screen", context: "product:fixture-app", ref: "", path: "", created_at: daysAgo(9) }],
    }),
    group("hooks:held", "hook-approval-requests", { source: "hook_requests", kind: "hook_approval", item_ids: ["opactreq_fx"], items: [{ id: "opactreq_fx", title: "write · fixture-capability", context: "fixture-agent", ref: "", path: "", created_at: daysAgo(0) }] }),
    group("sources:setup", "data-source-setup", { source: "data_sources", kind: "source_setup", item_ids: ["source_fx"], items: [{ id: "source_fx", title: "Add the fixture key", context: "Fixture source", ref: "", path: "", created_at: daysAgo(2) }] }),
    group("content|review", "product-content-queue", { needs_wording: true }),
    group("worktree|review", "worktree-retirement", { needs_wording: true }),
    // Skill proposals: one the source already reports under a rule, one it does not.
    group("capability-lifecycle-proposals|clp_covered", "capability-lifecycle-proposals", { item_ids: ["clp_covered"] }),
    group("capability-lifecycle-proposals|clp_open", "capability-lifecycle-proposals", { item_ids: ["clp_open"] }),
  ],
  set_aside: [
    { key: "product:product-results-due#duplicate", band: "set_aside", reason: "duplicate", reason_text: "Asks the same thing as another item that is still counted.", area: "product-results-due", source: "product", kind: "product_result", count: 1, item_ids: ["hyp_fx_dup"], closed: true, next_step: "The same question is still counted once in your list.", duplicate_of: "product-hypotheses|product:fixture-other" },
    { key: "product:product-results-due#placeholder_draft", band: "set_aside", reason: "placeholder_draft", reason_text: "Rests on a draft nobody wrote.", area: "product-results-due", source: "product", kind: "product_result", count: 1, item_ids: ["hyp_fx_draft"], closed: false, next_step: "Writing the draft is agent work." },
  ],
  totals: {
    total_read: 40, live_decisions: 12, set_aside: 25, closed_without_you: 18, waiting_on_others: 0, covered_by_rule: 1,
    set_aside_reasons: [
      { reason: "already_decided", reason_text: "Already decided.", count: 10, closed: true, next_step: "Nothing is left for you." },
      { reason: "window_closed", reason_text: "A held action whose approval window has closed.", count: 7, closed: true, next_step: "If the agent still needs it, it asks again." },
      { reason: "duplicate", reason_text: "Asks the same thing as another item.", count: 1, closed: true, next_step: "Counted once." },
      { reason: "placeholder_draft", reason_text: "Rests on a draft nobody wrote.", count: 4, closed: false, next_step: "Writing the draft is agent work." },
      { reason: "agent_work", reason_text: "An agent or job can do it.", count: 2, closed: false, next_step: "Not yours." },
      { reason: "on_hold", reason_text: "Held or waiting on something other than you.", count: 1, closed: false, next_step: "Its owner continues it." },
    ],
  },
  covered_by_rule: [{
    key: "capability-lifecycle-proposals|clp_covered#covered", band: "covered_by_rule", area: "capability-lifecycle-proposals", source: "reviews", kind: "promotion",
    count: 1, operator_summary: "1 proposal", source_group_key: "capability-lifecycle-proposals|clp_covered", item_ids: ["clp_covered"], records: [],
    rules: [{ grant_id: "grant_fx_skills", capability: "skill-update", expires_at: "2031-06-01T00:00:00Z" }],
    worker: "", next_step: "Nothing works them yet: no agent or scheduled job decides them, so they stay here until one is set up.",
  }],
  rules: { read: true, active_standing_grants: 2 },
  answered: [
    { area: "bench", item_id: "fx_1", question: "A new bench record needs your decision.", decision: "dismissed", decided_at: daysAgo(1), decided_by: "operator", still_waiting: false, outcome: "Dismissed on a fixture day. Recording it ran nothing else." },
    { area: "capability-lifecycle-proposals", item_id: "fx_2", question: "Review fixture hook events.", decision: "open_work", decided_at: daysAgo(30), decided_by: "fixture-agent:lane", work_id: "work_fx_started", work_state: "unknown", still_waiting: false, outcome: "Turned into work. Its state could not be read." },
  ],
};
const report = toAttentionReport(raw);

// 1. The newer fields arrive whole; an older tool's missing field is null, never empty.
assert.equal(report.groups[1].items.length, 3, "a group's items are kept");
assert.equal(report.groups[1].items[0].createdAt, daysAgo(4));
assert.deepEqual(report.rules, { read: true, activeStandingGrants: 2 });
assert.equal(report.coveredByRule[0].rules[0].grantId, "grant_fx_skills");
assert.equal(report.answered.length, 2);
assert.equal(report.setAside[0].duplicateOf, "product-hypotheses|product:fixture-other");
const older = toAttentionReport({ schema_version: "compound.attention.v0.1", groups: [] });
assert.equal(older.coveredByRule, null, "an older tool that sends no covered list is unknown, not empty");
assert.equal(older.rules, null);
assert.equal(older.answered, null);

// 2. The stopgap fold: the source's rule list first, the console's rule otherwise; never twice, never dropped.
const covered = coveredItemIds(report);
assert.equal(attentionShare(report.groups[7], covered), "covered", "a skill proposal the source covers is the rule's, not housekeeping");
assert.equal(attentionShare(report.groups[8], covered), "housekeeping", "one the source does not cover is still folded by the console rule");
assert.deepEqual(housekeepingGroups(report).map((g) => g.key), ["capability-lifecycle-proposals|clp_open"]);
const w = waitingOnYou(report);
assert.equal(w.groups, 5, "his decisions: held work, proposed memories, a product result, a held action, a data source");
assert.equal(w.wording, 2);
assert.equal(w.housekeeping.groups, 1);
assert.equal(w.coveredByRule.items, 1);
const shares = report.groups.map((g) => attentionShare(g, covered));
assert.equal(shares.filter((s) => s === "covered").length + w.housekeeping.groups + w.groups + w.wording, report.groups.length, "every needs-you group is counted in exactly one place");
assert.ok(!yourDecisionGroups(report).some((g) => g.area.startsWith("capability-lifecycle")), "no skill proposal is ever his");

// 3. The lines: the covered band says the rule and the source's own sentence; unread rules are said, never "no rule".
const words = describeWaiting(w);
assert.equal(words.coveredByRule, "Agents handle these under your rule: 1.");
assert.equal(words.rulesUnread, "");
assert.match(describeWaiting(waitingOnYou(toAttentionReport({ ...raw, rules: { read: false, active_standing_grants: 0 } }))).rulesUnread, /could not be read/);
assert.equal(describeWaiting(waitingOnYou(older)).rulesUnread, "", "a tool that does not say is not 'could not be read'");
const notesHTML = renderToStaticMarkup(createElement(WaitingNotes, {
  waiting: w, totals: report.totals, detail: true, openSetAside: true,
  wordingLinks: yourDecisionGroups(report).filter((g) => g.needs_wording).map((g) => ({ key: g.key, label: plainArea(g.area) })),
}));
const notes = text(notesHTML);
assert.match(notes, /Agents handle these under your rule: 1\. Skill changes · 1 item\. Your rule: Skill update until Jun 1, 2031\. grant_fx_skills Nothing works them yet/, notes);
assert.doesNotMatch(notes, /no rule covers/i);

// 4. D2: both numbers, each naming what it counts; the groups add up to their own numbers.
assert.equal(words.closedWithoutYou, "25 records were set aside and not counted as yours. 18 of them closed without you: nothing is left to decide.");
const groups = setAsideGroups(report.totals.reasons);
assert.deepEqual(groups.map((g) => [g.closed, g.count, g.reasons.reduce((n, r) => n + r.count, 0)]), [[true, 18, 18], [false, 7, 7]]);
assert.equal(groups[0].count, report.totals.closedWithoutYou, "the closed group is the closed number");
assert.equal(groups[0].count + groups[1].count, report.totals.setAside, "the groups add up to everything set aside");
assert.match(notesHTML, /<details id="set-aside" open=""/, "Today's 'See why' lands on the section open");
assert.match(notes, /Closed without you: nothing is left to decide: 18 records .*Not closed, and not yours now: someone or something else acts first: 7 records/);
const todayNotes = renderToStaticMarkup(createElement(WaitingNotes, { waiting: w, totals: report.totals, detail: false }));
assert.ok(todayNotes.includes(`href="${decideSetAsideHref().replace(/&/g, "&amp;")}"`), "Today's See why opens the section");
assert.equal(decideSetAsideHref(), "/reviews?open=set-aside#set-aside");

// 5. D3: the unworded decisions are links to their cards, not plain text.
assert.ok(notes.includes("2 more decisions are yours once an agent words them as a clear question. See them: Product content , Old worktrees to remove ."), "the unworded decisions are named");
assert.ok(/href="\/reviews\?decision=content%7Creview"[^>]*>Product content</.test(notesHTML) && notesHTML.includes('href="/reviews?decision=worktree%7Creview"'), "each one links its card");
const seven = Array.from({ length: 7 }, (_, i) => group(`d${i}`, "route-promotions"));
const listHTML = renderToStaticMarkup(createElement(NeedsYouList, { groups: seven, total: 7, act: null, mutationsEnabled: false, nowMs: NOW }));
assert.match(listHTML, /href="\/reviews\?decision=d5"[^>]*><span>2 more decisions on Decide/, "'N more' opens the first decision Today does not show");
assert.doesNotMatch(listHTML, /href="\/reviews"/, "never plain Decide, which opens row 1 again");

// 6. New kinds: a plain name and one question each; proposed memories open the Memory page's review.
for (const g of report.groups.slice(1, 5)) {
  const h = decisionHeadline(g, {}, NOW);
  assert.ok(h.question && !/record needs/.test(h.question), `${g.area}: one question`);
  assert.notEqual(plainArea(g.area), g.area.replace(/-/g, " "), `${g.area}: a plain name, not the area id`);
}
const mem = decisionHeadline(report.groups[1], {}, NOW);
assert.deepEqual(mem.answerAt, { label: "Judge them on the Memory page", href: memoryReviewHref() });
assert.equal(mem.countLabel, "3 proposed memories", "the count says what it counts, as the Memory page does");
const memRow = renderToStaticMarkup(createElement(NeedsYouList, { groups: [report.groups[1]], total: 1, act: null, mutationsEnabled: false, nowMs: NOW }));
assert.match(memRow, /href="\/memory#memory-review"[^>]*>Open/, "the memory row opens the Memory page's review section");
const result = decisionHeadline(report.groups[2], {}, NOW);
assert.deepEqual(result.about, { label: "Fixture App", href: "/apps/fixture-app", app: true }, "a product result names its app and links its page");
const card = decisionCard({ area: "memory-proposals", sample: {}, summary: report.groups[1].operator_summary, objectText: "", needsWording: false, recommendation: null, items: raw.groups[1].items });
assert.ok(card.yes.includes(memoryChoice("promote", "memory").means), "the yes uses the Memory page's words");
assert.equal(card.listed.length, 3);

// 7. D4: no raw option code, no contradiction, a derived no, and who writes what is missing.
const doneBrief = { options: [], recommendation: { option: "resolve_reviewed", confidence: 0.9, rationale: "Both fixture changes are already live." } };
const rec = recommendationWords(doneBrief);
assert.doesNotMatch(rec, /resolve_reviewed/);
assert.match(rec, /^Close it: the record says this is already done/);
const doneCard = decisionCard({ area: "route-promotions", sample: {}, summary: "", objectText: "", needsWording: false, recommendation: rec, recommendedOption: "resolve_reviewed" });
assert.doesNotMatch(doneCard.yes, /cannot run today/, "a done recommendation does not sit under a yes that says it cannot run");
assert.equal(recommendationWords({ options: [], recommendation: { option: "zz_unknown_code", confidence: 1, rationale: "A reason." } }), "A reason.", "an unknown code is left out");
const rule = decisionCard({ area: "decision-candidates", sample: {}, summary: "", objectText: "Keep fixtures small.", needsWording: false, recommendation: null });
assert.match(rule.no, /^Nothing changes/, "a no on a proposed rule keeps things as they are");
assert.ok(rule.about && rule.about.label, "a proposed rule names the part of the setup it is about");
assert.match(rule.silent, /Only the agent or tool that raised it can write that down\./);
const idea = decisionCard({ area: "product-hypotheses", sample: {}, summary: "", objectText: "", needsWording: false, recommendation: null });
assert.doesNotMatch(idea.no, /no way to record/i);

// 8. D4/D5: titles keep their punctuation; a cut id never shows.
assert.equal(cleanTitle("Hold destructive actions on cloud resources…."), "Hold destructive actions on cloud resources…");
assert.equal(cleanTitle("Fixture App has app_store_conn… evidence for a hypothesis."), "", "a raw id cut in half is never shown");
assert.equal(decisionHeadline(group("x", "decision-candidates", { operator_summary: "Decision lifecycle has candidate Ruling: Keep it small…. Review it?" }), {}, NOW).subject, "Keep it small…");

// 9. D4: the counter counts what the header counts; the others are named apart.
const asRows = (list) => list.map((g) => ({ headline: decisionHeadline(g, {}, NOW) }));
assert.equal(decisionCounter(asRows(yourDecisionGroups(report)), asRows(yourDecisionGroups(report))), "5 of 5 decisions · 2 to be worded first");

// 10. D5: Today's oldest wait and the oldest card use one function on one clock.
// Today passes a Date (lib/briefing.ts oldestCreatedAt); the card times the same moment from its record.
const oldestAt = new Date(daysAgo(128.6));
const header = text(renderToStaticMarkup(createElement(BriefingHeader, {
  now: new Date(NOW), waiting: { ...w, groups: 1 }, read: { status: "live", lastGoodAt: daysAgo(0), generatedAt: daysAgo(0), error: null, updating: false }, oldest: oldestAt,
})));
assert.ok(header.includes("The oldest has waited 128 days."), header);
assert.equal(waitingAge(oldestAt.toISOString(), NOW), "128 days", "the card says the same");

// 11. Product verdicts: the waiting list's answer, not a second rule.
assert.deepEqual(productVerdict(report, ["product:fixture-app"], []), { state: "due", decisionKey: "product:product-results-due", itemId: "hyp_fx_due" });
assert.deepEqual(productVerdict(report, ["product:fixture-other"], ["hyp_fx_dup"]), { state: "asked_elsewhere", decisionKey: "product-hypotheses|product:fixture-other" });
assert.deepEqual(productVerdict(report, ["product:fixture-draft"], ["hyp_fx_draft"]), { state: "not_listed" });
assert.deepEqual(productVerdict(null, ["product:fixture-app"]), { state: "unknown" });
assert.equal(decideItemHref("product:product-results-due", "hyp_fx_due"), "/reviews?decision=product%3Aproduct-results-due#decision-item-hyp_fx_due");

// 12. Answers: what came of each, the work linked, an unknown state said as unknown.
const answered = report.answered.map(answeredRow);
assert.equal(answered[0].by, "you");
assert.equal(answered[0].subject, "", "a template question is not repeated");
assert.equal(answered[1].by, "an agent");
assert.deepEqual(answered[1].work, { href: "/work/work_fx_started", state: "unknown" });
assert.equal(decideAnsweredHref(60), "/reviews?answers=60#answered");

console.log("decide-today cases ok: new kinds, covered band and its fold, set-aside groups that add up, linked lines, plain cards, one age, verdicts and answers");
