// Agents and Settings pages, against a synthetic compound binary:
//
// 1. One set of numbers. The executors projection sorts work into its own
//    agent profiles by a loose name match and puts every holder it does not
//    know into one "unassigned" bucket. The console regroups the rows by the
//    one agent key (lib/harness-label.ts), so each agent's count equals the
//    Work list its link opens, "no agent" means no holder, and your own items
//    are not an agent's. Working now and All agents print the same sentences
//    (lib/counts.ts) and link each number to its list.
//
// 2. Settings in plain words. Standing permissions: in force first, the ones
//    that cannot be undone and never expire on top, expired ones folded behind
//    a count, readable dates, one opening sentence. The stop control says what
//    exists and that it cannot be pressed here; no button, no type-to-confirm.
//    No command with a blank is shown; commands sit behind "For agents".
//
// Run as a child process by the wired traffic-board smoke (it owns a scratch
// home and cache folder and moves the read layer's global state). Directly:
//   bun scripts/agents-pages-cases.mjs
import assert from "node:assert/strict";
import { chmod, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { AppRouterContext } from "next/dist/shared/lib/app-router-context.shared-runtime.js";
import { PathnameContext, SearchParamsContext } from "next/dist/shared/lib/hooks-client-context.shared-runtime.js";

const scratch = await mkdtemp(join(tmpdir(), "compound-agents-pages-"));
const bin = join(scratch, "compound-stub");
const releaseReads = join(scratch, "release-reads");

// A fixed clock for the model cases; the page renders use the stub's own clock.
const NOW = Date.parse("2030-03-10T12:00:00Z");
const ns = (now, daysAgo) => String(BigInt(Math.round(now - daysAgo * 86_400_000)) * 1_000_000n);

/**
 * Open work, as the executors projection groups it. Holders the projection
 * does not know sit in its catch-all row; one item a loose name match put
 * under alpha belongs to another holder.
 */
function fixtureWork(now) {
  const w = (id, state, holder, days, extra = {}) => ({
    work_id: id, state, ...holder, ...(days === null ? {} : { claimed_at: ns(now, days) }),
    title: `Fixture ${id.replace(/^work_ap_/, "").replace(/_/g, " ")}`, next_action: "finish run", ...extra,
  });
  return {
    alpha: [
      w("work_ap_alpha_old", "running", { claimed_by: "alpha_agent" }, 9),
      w("work_ap_alpha_new", "claimed", { claimed_by: "alpha_agent:lane-1" }, 2),
      w("work_ap_alpha_check", "verifying", { owner: "alpha_agent" }, 1),
      w("work_ap_relay", "running", { claimed_by: "fixture-relay:lane-a" }, 1),
    ],
    beta: [
      w("work_ap_beta_held", "held", { claimed_by: "beta_agent:fixture-lane" }, 3, { next_action: "resolve hold" }),
      w("work_ap_beta_check", "verifying", { owner: "beta-agent" }, 10),
    ],
    unassigned: [
      w("work_ap_sched_old", "running", { owner: "fixture-scheduler" }, 12),
      w("work_ap_sched_ready", "ready", { owner: "fixture-scheduler" }, null),
      w("work_ap_yours", "claimed", { owner: "operator:fixture-person" }, 1),
      w("work_ap_nobody", "ready", {}, null),
    ],
  };
}

function executorsJson(now) {
  const work = fixtureWork(now);
  const row = (id, label, items, extra = {}) => ({
    id, label, status: "available", status_source: "profile_guide", aliases: [], current_work: items,
    running_count: items.length, assigned_count: items.length, needs_me_count: 0, lease_count: 0, ...extra,
  });
  return {
    schema_version: "compound.executors_projection.v0.1", generated_at: new Date(now).toISOString(),
    counts: { total: 4, open: 10, running: 8, ready: 2, needs_me: 1, assigned: 10, leases: 0 },
    delegated_sessions: [], delegated_counts: { total: 0, running: 0, exited: 0, refused: 0, unknown: 0 },
    executors: [
      row("alpha_agent", "Alpha Agent", work.alpha, { aliases: ["alpha_agent"] }),
      row("beta_agent", "Beta Agent", work.beta, { aliases: ["beta", "beta_agent", "beta-agent"] }),
      row("local_box", "Local Box", []),
      row("unassigned", "Unassigned", work.unassigned, { aliases: ["", "unassigned"], status: "waiting" }),
    ],
  };
}

await writeFile(bin, `#!/usr/bin/env bun
import { existsSync } from "node:fs";
const args = process.argv.slice(2).join(" ");
const now = Date.now();
const executorsJson = ${executorsJson.toString()};
const fixtureWork = ${fixtureWork.toString()};
const ns = ${ns.toString()};
if (args === "version") { console.log("synthetic"); process.exit(0); }
while (!existsSync(${JSON.stringify(releaseReads)})) await Bun.sleep(5);
const reply = {
  "executors --json": executorsJson(now),
  "attention --json": { schema_version: "compound.attention.v0.1", generated_at: new Date(now).toISOString(), counts: { needs_you: 1, needs_you_groups: 1, blocked_on_you: 1 }, groups: [] },
};
console.log(JSON.stringify(reply[args] ?? { generated_at: new Date(now).toISOString() }));
`);
await chmod(bin, 0o755);
process.env.COMPOUND_BIN = bin;
process.env.COMPOUND_HOME = join(scratch, "home");
process.env.COMPOUND_CONSOLE_MUTATIONS = "0";
process.env.COMPOUND_CONSOLE_OPERATOR_SESSION = "0";
process.env.COMPOUND_CONSOLE_CACHE_DIR = join(scratch, "cache");
process.env.COMPOUND_WORKTREE_REPO = "";
process.env.TMPDIR = scratch;

const { normalizeExecutorProjection } = await import("../src/components/agents/executor-normalize.ts");
const { buildRoster } = await import("../src/components/agents/agent-roster.ts");
const { countAgents, countInProgress, describeAgents, describeInProgress } = await import("../src/lib/counts.ts");
const { buildTraffic } = await import("../src/lib/traffic.ts");
const { buildWorkLensItems } = await import("../src/lib/work-views.ts");
const { filterWorkList } = await import("../src/lib/work-list.ts");
const { parseWorkListParams } = await import("../src/lib/links.ts");
const compound = await import("../src/lib/compound.ts");
const { default: Live } = await import("../src/app/live/page.tsx");
const { default: Agents } = await import("../src/app/executors/page.tsx");
const { summarizeGrants, readableDate } = await import("../src/components/settings/grants.ts");
const { AutonomySafety } = await import("../src/components/settings/AutonomySafety.tsx");
const { PolicyGroup } = await import("../src/components/settings/PolicyGroup.tsx");
const { MemoryLenses } = await import("../src/components/settings/MemoryLenses.tsx");

const router = Object.fromEntries(["push", "replace", "refresh", "back", "forward", "prefetch"].map((k) => [k, () => {}]));
const wrap = (path, element) =>
  renderToStaticMarkup(
    createElement(AppRouterContext.Provider, { value: router },
      createElement(PathnameContext.Provider, { value: path },
        createElement(SearchParamsContext.Provider, { value: new URLSearchParams() }, element))));
function visibleText(html) {
  return html
    .replace(/<script[\s\S]*?<\/script>/g, " ")
    .replace(/<style[\s\S]*?<\/style>/g, " ")
    .replace(/<[^>]+>/g, " ")
    .replace(/&quot;/g, '"').replace(/&#x27;|&#39;/g, "'").replace(/&amp;/g, "&").replace(/&lt;/g, "<").replace(/&gt;/g, ">")
    .replace(/\s+/g, " ");
}
const hrefs = (html) => [...html.matchAll(/href="([^"]+)"/g)].map((m) => m[1].replace(/&amp;/g, "&"));

try {
  /* ---------- 1. one set of numbers ---------- */
  const raw = executorsJson(NOW);
  const projection = normalizeExecutorProjection(raw);
  const byId = Object.fromEntries(projection.executors.map((row) => [row.id, row.currentWork.length]));
  assert.deepEqual(byId, { alpha_agent: 3, beta_agent: 2, fixture_scheduler: 2, fixture_relay: 1, operator: 1, unassigned: 1, local_box: 0 },
    "one row per agent key: the catch-all bucket splits into its real holders, a loosely matched item leaves alpha");
  assert.equal(projection.executors.find((row) => row.id === "unassigned").label, "No agent");
  assert.equal(projection.executors.find((row) => row.id === "operator").label, "You");
  assert.equal(projection.executors.find((row) => row.id === "beta_agent").label, "Beta Agent", "the projection's label stays the name");

  const agents = countAgents(projection.executors);
  assert.deepEqual(agents, { withWork: 4, idle: 1, heldItems: 8, noAgentItems: 1, yourItems: 1 });
  const agentsLine = describeAgents(agents);
  assert.equal(agentsLine, "4 agents hold 8 work items. 1 more work item has no agent. 1 more work item is held by you. 1 agent has nothing to do.");

  // Every number on a row equals the Work list its link opens (the real List filter).
  const open = Object.values(fixtureWork(NOW)).flat();
  const workRows = buildWorkLensItems(open, buildTraffic(projection, null), { nowMs: NOW });
  const listSize = (href) => filterWorkList(workRows, parseWorkListParams(new URL(href, "https://console.invalid").searchParams), { projectsKnown: true }).rows.length;
  const roster = buildRoster(projection.executors, NOW, 6);
  for (const agent of roster.holders) {
    assert.equal(listSize(agent.href.all), agent.items, `${agent.name}: holds = its Work list`);
    assert.equal(listSize(agent.href.progress), agent.inProgress.total, `${agent.name}: in progress = its list`);
    assert.equal(listSize(agent.href.stale), agent.stale ?? 0, `${agent.name}: not moved = its list`);
    assert.equal(listSize(agent.href.onHold), agent.onHold, `${agent.name}: need you = its list`);
  }
  assert.equal(roster.holders.reduce((n, a) => n + a.items, 0), open.length, "every open item has exactly one row");
  const inProgress = countInProgress(open, NOW);
  assert.deepEqual(roster.inProgress, inProgress, "the roster's in-progress count is counts.ts over the same items");
  assert.equal(inProgress.total, 7);
  assert.equal(inProgress.pickedUpOver7Days, 3);
  const beta = roster.holders.find((a) => a.key === "beta_agent");
  assert.deepEqual(beta.top.map((w) => w.workId), ["work_ap_beta_held", "work_ap_beta_check"], "on hold first, then not moved");

  /* ---------- page renders: Working now and All agents say the same ---------- */
  const coldAgents = visibleText(wrap("/executors", await Agents()));
  assert.match(coldAgents, /Reading who works on what for the first time/, "a cold All agents page says it is reading, never an empty fleet");
  await writeFile(releaseReads, "");
  await compound.settleProjectionReads();
  const liveHtml = wrap("/live", await Live());
  const agentsHtml = wrap("/executors", await Agents());
  const live = visibleText(liveHtml);
  const all = visibleText(agentsHtml);
  const pageProjection = normalizeExecutorProjection(await compound.runJson(["executors", "--json"]));
  const pageLine = describeAgents(countAgents(pageProjection.executors));
  const pageProgress = describeInProgress(countInProgress(pageProjection.executors.flatMap((row) => row.currentWork.map((w) => ({ work_id: w.workId, state: w.state, claimed_at: w.claimedAt }))), Date.now()));
  for (const [name, text] of [["Working now", live], ["All agents", all]]) {
    assert.ok(text.includes(pageLine), `${name} says "${pageLine}"`);
    assert.ok(text.includes(`${pageProgress.headline}: ${pageProgress.detail}`), `${name} says the in-progress split in counts.ts words`);
    assert.match(text, /3 of them were picked up more than 7 days ago/, `${name}: stale count`);
  }
  for (const [name, html] of [["Working now", liveHtml], ["All agents", agentsHtml]]) {
    const links = hrefs(html);
    for (const filter of ["progress", "running", "claimed", "verifying", "stale"]) {
      assert.ok(links.includes(`/work?lens=table&filter=${filter}`), `${name}: the ${filter} number opens its list`);
    }
  }
  assert.doesNotMatch(all, /Executors|Paused|Assg|\bRunning 8\b/, "no tile counts in-progress work as running or the catch-all as an agent");
  const agentLinks = hrefs(agentsHtml);
  for (const href of ["/work?lens=table&filter=all&owner=alpha_agent", "/work?lens=table&filter=progress&owner=alpha_agent",
    "/work?lens=table&filter=blocked&owner=beta_agent", "/work?lens=table&filter=stale&owner=fixture_scheduler", "/work?lens=table&filter=all&owner=unassigned"]) {
    assert.ok(agentLinks.includes(href), `All agents links ${href}`);
  }
  assert.match(all, /Set up but holding no work: Local Box\./);

  /* ---------- 2. settings in plain words ---------- */
  const day = 86_400_000;
  const iso = (offsetDays) => new Date(NOW + offsetDays * day).toISOString();
  const grants = [
    { id: "g_old", capability: "fixture.submit_entry", reversibility_ceiling: "irreversible", expires_at: iso(-40) },
    { id: "g_old2", capability: "fixture.post_note", reversibility_ceiling: "reversible", expires_at: iso(-3) },
    { id: "g_soon", capability: "fixture.edit_files", reversibility_ceiling: "reversible", expires_at: iso(5), target_path: "/srv/fixture/**" },
    { id: "g_forever", capability: "fixture.edit_files_anywhere", reversibility_ceiling: "reversible" },
    { id: "g_oneway", capability: "fixture.publish_release", reversibility_ceiling: "irreversible", expires_at: iso(30) },
    { id: "g_oneway_forever", capability: "fixture.delete_branches", reversibility_ceiling: "irreversible", reason: "Fixture cleanup." },
    { id: "g_later", capability: "fixture.weekly_digest", reversibility_ceiling: "reversible", not_before: iso(10), expires_at: iso(40) },
  ];
  const summary = summarizeGrants(grants, NOW);
  assert.deepEqual(summary.active.map((g) => g.id), ["g_oneway_forever", "g_oneway", "g_forever", "g_soon"],
    "in force only; cannot be undone and never expires first, then cannot be undone, then never expires, then soonest end");
  assert.deepEqual(summary.expired.map((g) => g.id), ["g_old2", "g_old"], "expired, newest first");
  assert.deepEqual(summary.upcoming.map((g) => g.id), ["g_later"]);
  assert.equal(summary.sentence, "Agents may do 4 things without asking; 2 of them cannot be undone.");
  assert.equal(readableDate(Date.parse("2030-03-15T00:00:00Z")), "15 Mar 2030");
  assert.equal(summary.active[3].when, `until ${readableDate(NOW + 5 * day)}`);
  assert.equal(summary.active[0].when, "never expires");

  const safetyHtml = renderToStaticMarkup(createElement(AutonomySafety, {
    halt: { installed: true, global_halt: false, disabled_class_count: 0 },
    authority: { installed: true, autonomy_level: "L1", mode: "propose_only" },
    grants: { grants, grant_count: grants.length },
    editable: { installed: false, nothing_editable: true, hard_deny_core: ["policy", "permissions", "ledger", "evaluator"] },
    nowMs: NOW,
  }));
  const safety = visibleText(safetyHtml);
  assert.match(safety, /Agents may do 4 things without asking; 2 of them cannot be undone\./, "one opening sentence");
  assert.match(safety, /Agents are not stopped\./);
  assert.match(safety, /Stopping them from here is not built yet\./, "the stop control says it cannot be pressed here");
  assert.doesNotMatch(safety, /Reveal halt|type: halt|governed|Kill switch/i, "no button, no ritual, no governed label");
  assert.doesNotMatch(safetyHtml, /<button[^>]*>(?!<code)/, "no control on the stop card that looks like it acts");
  assert.ok(safety.indexOf("Fixture delete branches") < safety.indexOf("Fixture publish release") && safety.indexOf("Fixture publish release") < safety.indexOf("Fixture edit files anywhere"), "most consequential first");
  assert.match(safetyHtml, /<details[^>]*><summary[^>]*>2 expired, no longer in force<\/summary>/, "expired grants folded behind their count");
  assert.ok(safetyHtml.indexOf("Fixture delete branches") < safetyHtml.indexOf("2 expired"), "in force before expired");
  assert.match(safety, /Agents suggest changes and ask you before they change files on this machine\./, "autonomy level in one sentence");
  assert.match(safetyHtml, /<details[^>]*><summary[^>]*>For agents: commands and technical record<\/summary>[\s\S]*autonomy-halt --global --reason/, "the stop command sits behind For agents");
  const policyHtml = renderToStaticMarkup(createElement(PolicyGroup, {
    placement: { placement_guard_mode: "observe", block_owner_ids: ["fixture-core"], owner_count: 3 },
    frontier: { family_count: 2, deferred_count: 0, approval_required_open_count: 1, families: [] },
    prompts: { prompt_count: 1, high_severity_count: 1, prompts: [{ id: "p1", question: "Should the fixture app ship its next release?", severity: "high" }] },
    holds: { hold_count: 2, holds: [{ goal_id: "goal_a", app_id: "fixture-app", objective: "Fixture App release 2" }, { goal_id: "goal_b", objective: "Fixture service upgrade" }] },
    decisions: { record_count: 5, counts_by_status: { compound_primary: 4, compound_parked: 1 }, counts_by_kind: {} },
  }));
  const policy = visibleText(policyHtml);
  assert.match(policy, /Should the fixture app ship its next release\?/);
  assert.match(policy, /No page in the console answers these yet/, "a question with no place to answer says so");
  assert.ok(hrefs(policyHtml).includes("/apps/fixture-app"), "a release hold links to its app");
  assert.match(policy, /Fixture service upgrade/, "every hold is listed");
  const memory = visibleText(renderToStaticMarkup(createElement(MemoryLenses, { engine: { profile: "breadth", configured: true } })));
  assert.match(memory, /Memory search uses the "breadth" setting: it searches Compound's own records first/);
  for (const [name, html] of [["safety", safetyHtml], ["rules", policyHtml], ["memory", renderToStaticMarkup(createElement(MemoryLenses, { engine: null }))]]) {
    assert.doesNotMatch(html, /&lt;[a-z][\w.-]*&gt;/, `${name}: no command with a blank to fill in`);
  }

  console.log("agents-pages cases ok: one row per agent key, each count equals its Work list, Working now and All agents agree; settings say what agents may do in plain words");
} catch (error) {
  console.error(error);
  process.exitCode = 1;
} finally {
  // Release held reads so no stub child outlives a failed case.
  await writeFile(releaseReads, "").catch(() => {});
  await rm(scratch, { recursive: true, force: true });
}
process.exit(process.exitCode ?? 0);
