// Agents and Settings pages, against a synthetic compound binary:
//
// 1. One set of numbers. The executors projection sorts work into its own
//    agent profiles by a loose name match, puts every holder it does not
//    know into one "unassigned" bucket, leaves out suggested and ready work,
//    and is read at another moment than the Work List. The console counts
//    every agent's work over the Work page's own open list (workbench), one
//    row per agent key with lane spellings folded (lib/harness-label.ts), so
//    each number equals the Work list its link opens, "no agent" means no
//    holder, your own items are not an agent's, and work nobody picked up is
//    named and linked. Every linked number on Working now, All agents and
//    Waiting is checked against the length of the list it opens.
//
// 3. Working now tells the truth about motion: nothing animates from the
//    work record's own state, the latest work is in pick-up order with plain
//    next steps, and each agent's numbers link to its lists. All agents lists
//    the oldest work that has not moved, linked, and never says "clear it".
//
// 5. Models and cost: each waiting model change links to its own decision on
//    Decide, holds are one table (each row linked), the chosen tab is in the
//    address, and no command with a blank is shown.
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
import { readFileSync } from "node:fs";
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
      w("work_ap_alpha_check", "verifying", { owner: "alpha_agent" }, 1, { next_action: "attach the final verified candidate as commit:<full-sha>" }),
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

/**
 * The Work page's open list (workbench), read a moment after the executors
 * copy: one item has moved on to its proof check since, one is held by a
 * lane spelled with hyphens, and suggested and ready work (which the
 * executors projection leaves out) is in it.
 */
function fixtureOpen(now) {
  const items = Object.values(fixtureWork(now)).flat().map((item) => ({ ...item }));
  items.find((item) => item.work_id === "work_ap_alpha_old").state = "verifying";
  const extra = (id, state, holder, days) => ({
    work_id: id, state, ...holder, ...(days === null ? {} : { claimed_at: ns(now, days) }),
    desired_outcome: `Fixture ${id.replace(/^work_ap_/, "").replace(/_/g, " ")}`, next_action: "finish run",
  });
  return [
    ...items,
    extra("work_ap_alpha_lane", "running", { claimed_by: "alpha-agent-fixture-lane" }, 3),
    extra("work_ap_alpha_queued", "ready", { owner: "alpha_agent" }, null),
    extra("work_ap_alpha_blocked", "blocked", { claimed_by: "alpha_agent" }, 2),
    extra("work_ap_yours_suggested", "proposed", { owner: "operator:fixture-person" }, null),
  ];
}

/**
 * The waiting list: of the two open items on hold, one is held for your
 * answer and one is blocked on something outside you. The CLI decides which
 * from more of the record than the open list carries.
 */
function attentionJson(now) {
  const at = new Date(now).toISOString();
  const group = (key, band, kind, ids, summary) => ({
    key: `workbench:${key}`, area: `workbench-${key}`, band, source: "workbench", kind, count: ids.length, item_ids: ids,
    operator_summary: summary, operator_unlock: "", blast_radius: "local_reversible", reversibility: "reversible", leverage: 1,
    oldest_created_at: at, action: null, terminal_reason: "", freshness: { generated_at: at, source: "workbench" },
  });
  return {
    schema_version: "compound.attention.v0.1", generated_at: at, counts: { needs_you: 1, needs_you_groups: 1, blocked_on_you: 1 },
    groups: [
      group("blocked-on-you", "needs_you", "gate_admit", ["work_ap_beta_held"], "1 open work item is held for your answer."),
      group("waiting-on-others", "watching", "waiting_on_others", ["work_ap_alpha_blocked"], "1 open work item waits on something outside you."),
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
const fixtureOpen = ${fixtureOpen.toString()};
const attentionJson = ${attentionJson.toString()};
const ns = ${ns.toString()};
if (args === "version") { console.log("synthetic"); process.exit(0); }
while (!existsSync(${JSON.stringify(releaseReads)})) await Bun.sleep(5);
const lane = (stamp) => ({ state: "unknown", remaining: 0, confirmation_required: true, message: "lane capacity --confirm required: state=unknown data_row_timestamp=" + stamp + " data_ref=fixture" });
const actor = (name, doing, stamp) => ({ actor: name, doing, can_continue: false, wait_reason: "capacity_confirmation_required", blocked_on: [], lane: lane(stamp), open: 10, in_progress: 7 });
const reply = {
  "coordination --json": {
    schema_version: "compound.coordination.v0.1", generated_at: new Date(now).toISOString(), actor: "alpha_agent:lane-1",
    me: actor("alpha_agent:lane-1", { work_id: "work_ap_alpha_new", title: "Fixture alpha new", state: "claimed" }, "2030-03-01T00:00:00Z"),
    others: [
      actor("beta_agent:fixture-lane", { work_id: "work_ap_beta_held", title: "Fixture beta held", state: "held" }, "2030-03-05T00:00:00Z"),
      actor("fixture-scheduler", null, "unknown"),
    ],
    waits: [], integrity: [], operator_coordination: { items: [] },
  },
  "executors --json": executorsJson(now),
  "workbench --json": { generated_at: new Date(now).toISOString(), open: fixtureOpen(now), recent: [] },
  "attention --json": attentionJson(now),
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
const { buildRoster, latestPickedUp, oldestNotMoved } = await import("../src/components/agents/agent-roster.ts");
const { isActiveRun } = await import("../src/components/live/LiveHarnessCanvas.tsx");
const { countAgents, countInProgress, countWaitingWork, describeAgents, describeInProgress, waitingWork } = await import("../src/lib/counts.ts");
const { toAttentionReport } = await import("../src/lib/attention.ts");
const { buildTraffic } = await import("../src/lib/traffic.ts");
const { buildWorkLensItems } = await import("../src/lib/work-views.ts");
const { filterWorkList } = await import("../src/lib/work-list.ts");
const { parseWorkListParams, workListHref } = await import("../src/lib/links.ts");
const compound = await import("../src/lib/compound.ts");
const { default: Live } = await import("../src/app/live/page.tsx");
const { default: Agents } = await import("../src/app/executors/page.tsx");
const { default: Waiting } = await import("../src/app/traffic/page.tsx");
const { default: Work } = await import("../src/app/work/page.tsx");
const { summarizeGrants, readableDate } = await import("../src/components/settings/grants.ts");
const { AutonomySafety } = await import("../src/components/settings/AutonomySafety.tsx");
const { PolicyGroup } = await import("../src/components/settings/PolicyGroup.tsx");
const { MemoryLenses } = await import("../src/components/settings/MemoryLenses.tsx");
const { PromotionBand } = await import("../src/components/routing/PromotionBand.tsx");
const { BenchPanel } = await import("../src/components/routing/BenchPanel.tsx");
const { routingTabOf } = await import("../src/components/routing/RoutingTabs.tsx");
const { DelegatedSessions } = await import("../src/components/agents/DelegatedSessions.tsx");
const { WorktreeInventory } = await import("../src/components/agents/WorktreeInventory.tsx");
const { CapabilityAttentionStrip } = await import("../src/components/capabilities/CapabilityAttentionStrip.tsx");
const { default: Views } = await import("../src/app/representation/page.tsx");

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
  // The executors copy alone: its catch-all bucket splits into its real holders and only picked-up work is held.
  const execOnly = normalizeExecutorProjection(raw);
  assert.deepEqual(Object.fromEntries(execOnly.executors.map((row) => [row.id, row.currentWork.length])),
    { alpha_agent: 3, beta_agent: 2, fixture_scheduler: 1, fixture_relay: 1, operator: 1, local_box: 0 },
    "one row per agent key: the catch-all bucket splits into its real holders, a loosely matched item leaves alpha, ready work is held by nobody");

  // What the pages count: the executors profiles, with the work of the Work page's own open list.
  const openList = fixtureOpen(NOW);
  const projection = normalizeExecutorProjection(raw, openList);
  const byId = Object.fromEntries(projection.executors.map((row) => [row.id, row.currentWork.length]));
  assert.deepEqual(byId, { alpha_agent: 5, beta_agent: 2, fixture_scheduler: 1, fixture_relay: 1, operator: 1, local_box: 0 },
    "counted over the open list: the lane spelled with hyphens folds into its agent, the item that moved on counts where it is now");
  assert.equal(projection.executors.find((row) => row.id === "operator").label, "You");
  assert.equal(projection.executors.find((row) => row.id === "beta_agent").label, "Beta Agent", "the projection's label stays the name");

  const agents = countAgents(projection.executors, openList);
  assert.deepEqual(agents, { withWork: 4, idle: 1, heldItems: 9, noAgentItems: 0, yourItems: 1, notPickedUp: 4 });
  const agentsLine = describeAgents(agents);
  assert.equal(agentsLine, "4 agents hold 9 work items. 1 more work item is held by you. 4 more open work items are suggested or ready to start: nobody has picked them up yet. 1 agent has nothing to do.");

  // Every number on a row equals the Work list its link opens (the real List filter over the same open list).
  const workRows = buildWorkLensItems(openList, buildTraffic(projection, null), { nowMs: NOW });
  const listSize = (href) => filterWorkList(workRows, parseWorkListParams(new URL(href, "https://console.invalid").searchParams), { projectsKnown: true }).rows.length;
  const roster = buildRoster(projection.executors, NOW, 6, openList);
  for (const agent of roster.holders) {
    assert.equal(listSize(agent.href.all), agent.items, `${agent.name}: holds = its Work list`);
    assert.equal(listSize(agent.href.progress), agent.inProgress.total, `${agent.name}: in progress = its list`);
    assert.equal(listSize(agent.href.stale), agent.stale ?? 0, `${agent.name}: not moved = its list`);
    assert.equal(listSize(agent.href.onHold), agent.onHold, `${agent.name}: need you = its list`);
  }
  assert.equal(listSize(workListHref({ filter: "queued" })), agents.notPickedUp, "not picked up yet = its list");
  assert.equal(roster.holders.reduce((n, a) => n + a.items, 0) + agents.notPickedUp, openList.length,
    "every open item is held by exactly one row or counted as not picked up");
  const inProgress = countInProgress(openList, NOW);
  assert.deepEqual(roster.inProgress, inProgress, "the roster's in-progress count is counts.ts over the Work page's open list");
  assert.equal(inProgress.total, 8);
  assert.equal(inProgress.pickedUpOver7Days, 3);
  const beta = roster.holders.find((a) => a.key === "beta_agent");
  assert.deepEqual(beta.top.map((w) => w.workId), ["work_ap_beta_held", "work_ap_beta_check"], "on hold first, then not moved");

  // "Need you" is the waiting list's own sorting: held for your answer, not blocked on something else.
  const waitingIds = waitingWork(toAttentionReport(attentionJson(NOW)));
  assert.deepEqual({ you: [...waitingIds.you], others: [...waitingIds.others] }, { you: ["work_ap_beta_held"], others: ["work_ap_alpha_blocked"] });
  assert.deepEqual(countWaitingWork(openList, waitingIds), { you: 1, others: 1 });
  assert.deepEqual(countWaitingWork(openList, null), { you: 2, others: 0 }, "before the waiting list is read, on hold counts by state");
  const waitRows = buildWorkLensItems(openList, buildTraffic(projection, null), { nowMs: NOW, waiting: waitingIds });
  const waitListSize = (href) => filterWorkList(waitRows, parseWorkListParams(new URL(href, "https://console.invalid").searchParams), { projectsKnown: true }).rows.length;
  const waitRoster = buildRoster(projection.executors, NOW, 6, { openWork: openList, waiting: waitingIds });
  for (const agent of waitRoster.holders) assert.equal(waitListSize(agent.href.onHold), agent.onHold, `${agent.name}: need you = its list`);
  assert.equal(waitRoster.holders.find((a) => a.key === "alpha_agent").onHold, 0, "work blocked on something else does not need you");
  assert.equal(waitListSize(workListHref({ filter: "blocked" })), 1);
  assert.equal(waitListSize(workListHref({ filter: "others" })), 1);

  /* ---------- page renders: Working now and All agents say the same ---------- */
  const coldAgents = visibleText(wrap("/executors", await Agents()));
  assert.match(coldAgents, /Reading who works on what for the first time/, "a cold All agents page says it is reading, never an empty fleet");
  await writeFile(releaseReads, "");
  await compound.settleProjectionReads();
  const liveHtml = wrap("/live", await Live());
  const agentsHtml = wrap("/executors", await Agents());
  const live = visibleText(liveHtml);
  const all = visibleText(agentsHtml);
  const pageOpen = (await compound.runJson(["workbench", "--json"])).open;
  const pageProjection = normalizeExecutorProjection(await compound.runJson(["executors", "--json"]), pageOpen);
  const pageLine = describeAgents(countAgents(pageProjection.executors, pageOpen));
  const pageProgress = describeInProgress(countInProgress(pageOpen, Date.now()));
  for (const [name, text] of [["Working now", live], ["All agents", all]]) {
    assert.ok(text.includes(pageLine), `${name} says "${pageLine}"`);
    assert.ok(text.includes(`${pageProgress.headline}: ${pageProgress.detail}`), `${name} says the in-progress split in counts.ts words`);
    assert.match(text, /3 of them were picked up more than 7 days ago/, `${name}: stale count`);
  }
  for (const [name, html] of [["Working now", liveHtml], ["All agents", agentsHtml]]) {
    const links = hrefs(html);
    for (const filter of ["progress", "running", "claimed", "verifying", "stale", "queued"]) {
      assert.ok(links.includes(`/work?lens=table&filter=${filter}`), `${name}: the ${filter} number opens its list`);
    }
  }
  // Every number that links to the Work List equals the length of the list it opens, over the page's own open list.
  const pageWaiting = waitingWork(toAttentionReport(await compound.runJson(["attention", "--json"])));
  const pageRows = buildWorkLensItems(pageOpen, buildTraffic(pageProjection, null), { nowMs: Date.now(), waiting: pageWaiting });
  const pageListSize = (href) => filterWorkList(pageRows, parseWorkListParams(new URL(href, "https://console.invalid").searchParams), { projectsKnown: true }).rows.length;
  const linkedNumbers = (html) => [...html.matchAll(/<a [^>]*href="(\/work\?[^"]+)"[^>]*>([\s\S]*?)<\/a>/g)]
    .map((m) => ({ href: m[1].replace(/&amp;/g, "&"), text: visibleText(m[2]).trim() }))
    .map((link) => ({ ...link, n: (link.text.match(/^(?:See all )?(\d[\d,]*)\b/) ?? link.text.match(/^\+\d[\d,]* more: all (\d[\d,]*)\b/))?.[1] }))
    .filter((link) => link.n !== undefined);
  assert.match(live, /1 work item waits for your decision\. 1 more work item waits on something else, not on you\./, "Working now names the two apart");
  for (const [name, html] of [["Working now", liveHtml], ["All agents", agentsHtml]]) {
    const numbers = linkedNumbers(html);
    assert.ok(numbers.length >= 8, `${name}: its numbers link to Work lists`);
    for (const link of numbers) {
      assert.equal(pageListSize(link.href), Number(link.n.replace(/,/g, "")), `${name}: "${link.text}" opens ${link.href}, a list of the same length`);
    }
  }
  // Work's header: the same split as Today and Decide (waiting for you apart from waiting on something else), each number its list.
  await Work({ searchParams: Promise.resolve({}) });
  await compound.settleProjectionReads();
  const workHtml = wrap("/work", await Work({ searchParams: Promise.resolve({}) }));
  const work = visibleText(workHtml);
  assert.match(work, /1 on hold, waiting for you · 1 waits on something else, not on you/, "Work names the two apart");
  const workNumbers = linkedNumbers(workHtml);
  assert.ok(workNumbers.length >= 6, "Work's header numbers link to their lists");
  for (const link of workNumbers) {
    assert.equal(pageListSize(link.href), Number(link.n.replace(/,/g, "")), `Work: "${link.text}" opens ${link.href}, a list of the same length`);
  }
  // Work's agent menu: one entry per agent (lane spellings folded), each count its open-work list.
  const menu = [...workHtml.replace(/<!-- -->/g, "").matchAll(/<option value="([^"]+)">([^<]*?) \((\d+) open\)<\/option>/g)];
  assert.ok(menu.length >= 4, "the agent menu lists the agents");
  assert.ok(!menu.some((m) => /fixture_lane/.test(m[1])), "a lane spelled with hyphens is not a menu entry of its own");
  for (const [, key, name, n] of menu) {
    assert.equal(pageListSize(`/work?lens=table&filter=all&owner=${key}`), Number(n), `Work's menu: ${name} (${n} open) equals its list`);
  }
  // The same numbers on Work and on the Agents pages.
  for (const [filter, words] of [["running", "running"], ["claimed", "picked up"], ["verifying", "waiting for a proof check"]]) {
    const n = pageListSize(`/work?lens=table&filter=${filter}`);
    assert.ok(work.includes(`${n} ${words}`) && live.includes(`${n} ${words}`) && all.includes(`${n} ${words}`), `${words}: ${n} on Work, Working now and All agents`);
  }
  assert.doesNotMatch(all, /Executors|Paused|Assg|\bRunning 8\b/, "no tile counts in-progress work as running or the catch-all as an agent");
  const agentLinks = hrefs(agentsHtml);
  for (const href of ["/work?lens=table&filter=picked&owner=alpha_agent", "/work?lens=table&filter=progress&owner=alpha_agent",
    "/work?lens=table&filter=blocked&owner=beta_agent", "/work?lens=table&filter=stale&owner=fixture_scheduler", "/work?lens=table&filter=picked&owner=operator"]) {
    assert.ok(agentLinks.includes(href), `All agents links ${href}`);
  }
  assert.doesNotMatch(all, /Alpha Agent Fixture Lane/, "a lane spelled with hyphens is not shown as an agent of its own");
  assert.match(all, /Set up but holding no work: Local Box\./);

  /* ---------- 4. waiting on each other: one sentence, holders linked, same numbers ---------- */
  const coldWaiting = visibleText(wrap("/traffic", await Waiting({ searchParams: Promise.resolve({}) })));
  assert.match(coldWaiting, /Reading [a-z ]+ for the first time/, "a cold read says it is reading, never an empty list");
  await compound.settleProjectionReads();
  const waitingHtml = wrap("/traffic", await Waiting({ searchParams: Promise.resolve({}) }));
  const waiting = visibleText(waitingHtml);
  assert.match(waiting, /3 agent sessions are waiting\. All of them wait for the same thing: a capacity check on its model that asks for a confirmation nobody gives yet\. Their last capacity readings date from 1 Mar 2030 to 5 Mar 2030; 1 has none at all\./);
  assert.match(waiting, /Nothing in this console can give what they wait for/);
  assert.ok(hrefs(waitingHtml).includes("/work?lens=table&filter=all&owner=alpha_agent"), "an agent whose session holds work links to its list");
  assert.ok(hrefs(waitingHtml).includes("/work/work_ap_beta_held"), "each held item links to its page");
  assert.doesNotMatch(waiting, /view as|shared queue|10 open|coordination stale|stream live|\bMe\b|Others/, "no per-card queue numbers, no 'Me', one freshness chip");
  assert.ok(waiting.includes(pageLine), "the waiting page says the same agent count as Working now");
  assert.ok(waiting.includes(`${pageProgress.headline}: ${pageProgress.detail}`), "and the same in-progress split");
  for (const link of linkedNumbers(waitingHtml)) {
    assert.equal(pageListSize(link.href), Number(link.n.replace(/,/g, "")), `Waiting: "${link.text}" opens ${link.href}, a list of the same length`);
  }

  /* ---------- 3. motion, order and links on Working now and All agents ---------- */
  assert.equal(isActiveRun({ runStatus: "", state: "running" }), false, "a work record saying running does not animate");
  assert.equal(isActiveRun({ runStatus: "running" }), true, "a run record saying running does");
  assert.deepEqual(latestPickedUp(execOnly.executors, NOW, 6).rows.map((w) => w.workId).slice(3),
    ["work_ap_alpha_new", "work_ap_beta_held", "work_ap_alpha_old"], "latest work in pick-up order, newest first");
  assert.deepEqual(oldestNotMoved(execOnly.executors, NOW, 10).map((w) => w.workId),
    ["work_ap_sched_old", "work_ap_beta_check", "work_ap_alpha_old"], "oldest not-moved work first");
  assert.doesNotMatch(liveHtml, /data-live="true"|runbar|running now|Running now/, "nothing on Working now moves without a run record");
  assert.match(live, /Record the final checked commit for it\./, "next steps in plain words");
  assert.doesNotMatch(live, /commit:<full-sha>/);
  assert.match(live, /not a live feed/, "the page says it is the last read, not a live feed");
  {
    // The agent panel's counts use the canvas's own colours (ink on the canvas), not blue on grey.
    const canvasSource = readFileSync(new URL("../src/components/live/LiveHarnessCanvas.tsx", import.meta.url), "utf8");
    const laneCount = canvasSource.slice(canvasSource.indexOf("function LaneCount"), canvasSource.indexOf("function Canvas"));
    assert.ok(laneCount.includes("text-[var(--canvas-ink)]") && !laneCount.includes("bg-soft") && !laneCount.includes("text-work"), "panel numbers read on the dark canvas");
  }
  for (const href of ["/work?lens=table&filter=picked&owner=alpha_agent", "/work?lens=table&filter=progress&owner=alpha_agent", "/work/work_ap_alpha_check"]) {
    assert.ok(hrefs(liveHtml).includes(href), `Working now links ${href}`);
  }
  assert.doesNotMatch(all, /clear it|Archive lease history|Copy command|Lease pressure/i, "no instruction to act where no action exists");
  assert.match(all, /3 work items were picked up more than 7 days ago and are still not finished\. Nothing moves them on its own yet/);
  assert.ok(agentLinks.indexOf("/work/work_ap_sched_old") < agentLinks.indexOf("/work/work_ap_alpha_old"), "the oldest stale item is listed first, linked");

  /* ---------- 6. All agents carries its weight: old sessions folded, no start-up hint, cost in one place ---------- */
  const session = (i, status, extra = {}) => ({ workId: `work_ap_session_${i}`, lane: "fixture-lane", session: `s${i}`, status, holder: "", runAttemptId: "", runAttemptStatus: "", progress: null, refusedReason: "", spawnedAt: "", lastSteerAt: "", exitedAt: "", observedExit: "", ...extra });
  const sessionRows = [session(0, "running"), session(1, "exited", { observedExit: "abandoned" }), ...Array.from({ length: 14 }, (_, i) => session(i + 2, "exited", { observedExit: "succeeded" }))];
  const sessionsHtml = renderToStaticMarkup(createElement(DelegatedSessions, { rows: sessionRows, counts: { total: 16, running: 1, exited: 15, refused: 0, unknown: 0 } }));
  assert.match(sessionsHtml, /<details[^>]*><summary[^>]*>5 earlier sessions<\/summary>/, "running and the latest 10 shown, the rest folded behind their count");
  assert.ok(sessionsHtml.indexOf("Ended unfinished") > 0 && sessionsHtml.indexOf("Ended unfinished") < sessionsHtml.indexOf("text-good"), "an abandoned session is not shown as done");
  assert.ok(hrefs(sessionsHtml).includes("/work/work_ap_session_0"), "a session's work links to its page");
  assert.match(sessionsHtml, /overflow-x-auto/, "wide tables scroll instead of cutting columns off");
  assert.equal(renderToStaticMarkup(createElement(WorktreeInventory, { repo: null, read: null })), "", "no start-up hint when no repository was chosen");
  assert.doesNotMatch(all, /COMPOUND_WORKTREE_REPO|not wired|cost-doctor|path\/to/, "All agents opens with its answer, not a start-up hint or a command with a blank");
  assert.ok(agentLinks.includes("/routing?tab=traffic"), "cost is one link to Models and cost");

  /* ---------- 7. back room: Skills and tools, View library ---------- */
  const strip = renderToStaticMarkup(createElement(CapabilityAttentionStrip, { items: [{ area: "fixture-empty", label: "Empty", count: 0 }, { area: "capability-lifecycle-proposals", label: "Skill changes", count: 3 }] }));
  assert.ok(hrefs(strip).includes("/reviews?area=capability-lifecycle-proposals"), "Review in Decide opens the matching decisions, not the whole queue");
  assert.doesNotMatch(readFileSync(new URL("../src/app/map/page.tsx", import.meta.url), "utf8"), /\/projects/, "no link to the retired project map");
  await Views();
  await compound.settleProjectionReads();
  const views = visibleText(wrap("/representation", await Views()));
  assert.match(views, /A workbench for the agents that build this console/, "the View library says what it is");
  assert.doesNotMatch(views, /Compare the available views/);
  // Each card leads with the work's plain title; the id is small text, never the title.
  assert.match(views, /Fixture alpha lane [A-Za-z ,]*work_ap_alpha_lane/, "a card is titled by the work's own words, its id under it");
  const { default: LineageIndex } = await import("../src/app/map/lineage/page.tsx");
  assert.match(visibleText(wrap("/map/lineage", LineageIndex())), /A tool for agents\. You reach a record's history from the page that names it/, "the history page says who it is for");

  /* ---------- 5. models and cost ---------- */
  const routeGroup = (key, title, extra = {}) => ({
    key, glyph: "R", areaPlain: "Model upgrades", areaLabel: "Model upgrades", count: 1, title, capability: { kind: "unavailable" },
    unlock: "", body: "", consequences: null, face: { authority: null, unavailableReason: "unavailable.", freshness: { generatedAt: "", source: "compound attention --json" } }, ageLabel: "12 days ago", old: false,
    act: { mode: "copy", reason: "unavailable." }, authorityBound: false, evidence: [], sampleId: key, ...extra,
  });
  const bandHtml = wrap("/routing", createElement(PromotionBand, {
    needsYou: [routeGroup("route-promotions|fx_one", "Fixture model ran 40 runs, 2% failed. Promote it?")],
    holdRecord: [routeGroup("route-promotions|fx_hold_a", "Fixture model A: 0/0 runs"), routeGroup("route-promotions|fx_hold_b", "Fixture model B: 3/9 runs", { old: true })],
    classA: false, mutationsEnabled: false,
  }));
  const band = visibleText(bandHtml);
  for (const key of ["route-promotions|fx_one", "route-promotions|fx_hold_a", "route-promotions|fx_hold_b"]) {
    assert.ok(hrefs(bandHtml).includes(`/reviews?decision=${encodeURIComponent(key)}`), `the model change ${key} links to its decision`);
  }
  assert.equal((bandHtml.match(/<tbody>([\s\S]*?)<\/tbody>/)?.[1].match(/<tr\b/g) ?? []).length, 2, "holds are one table, one row each");
  assert.doesNotMatch(band, /No one-click|\.\.|unavailable|Clean seam|Record a hold/, "no repeated hold text, no doubled period, no dead control text");
  assert.doesNotMatch(bandHtml, /<code/, "no command on a model change with no action");
  assert.doesNotMatch(renderToStaticMarkup(createElement(BenchPanel)), /&lt;|<code/, "no command with a blank on Bench");
  assert.equal(routingTabOf("traffic"), "traffic");
  assert.equal(routingTabOf("made-up"), "lanes", "an unknown tab opens the default");

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

  console.log("agents-pages cases ok: one row per agent key over the Work page's open list, every linked number equals its Work list, Working now and All agents agree; settings say what agents may do in plain words");
} catch (error) {
  console.error(error);
  process.exitCode = 1;
} finally {
  // Release held reads so no stub child outlives a failed case.
  await writeFile(releaseReads, "").catch(() => {});
  await rm(scratch, { recursive: true, force: true });
}
process.exit(process.exitCode ?? 0);
