import assert from "node:assert/strict";
import { chmod, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { renderToStaticMarkup } from "react-dom/server";
import { createElement } from "react";
import { AppRouterContext } from "next/dist/shared/lib/app-router-context.shared-runtime.js";
import { SearchParamsContext, PathnameContext } from "next/dist/shared/lib/hooks-client-context.shared-runtime.js";

const scratch = await mkdtemp(join(tmpdir(), "compound-memory-read-"));
const bin = join(scratch, "compound-stub");
await writeFile(bin, `#!/usr/bin/env bun
const mode = process.env.MEMORY_READ_SMOKE_MODE;
require("node:fs").appendFileSync(${JSON.stringify(join(scratch, "argv.log"))}, process.argv.slice(2).join(" ") + "\\n");
if (process.argv[2] === "version") { console.log("synthetic"); process.exit(0); }
if (mode === "fail") { console.error("synthetic memory projection unavailable"); process.exit(7); }
if (mode === "slow") await Bun.sleep(16000);
if (process.argv[2] === "memory" && process.argv[3] === "canon") {
  // Accepted memories by kind: a different population from candidate records.
  const distribution = mode === "empty" ? {decision:0,status:0} : {decision:5,principle:0,status:15,unclassified:3};
  console.log(JSON.stringify({schema_version:"compound.memory.canon.v0.1",distribution,total:Object.values(distribution).reduce((a,b)=>a+b,0)}));
  process.exit(0);
}
let items = mode === "empty" ? [] : [
 {id:"cand_action",entity:"product:synthetic",state:"proposed",predicate:"status",object:"observed",confidence:0.8},
 {id:"cand_source",entity:"knowledge:synthetic",state:"proposed",predicate:"source",object:"evidence",confidence:0.8},
 {id:"cand_law",entity:"decision:synthetic",state:"parked",claim_type:"decision",predicate:"choice",object:"Keep evidence",confidence:0.8},
  {id:"cand_promoted",state:"promoted",created_at:new Date().toISOString()},
  {id:"cand_prior",state:"promoted",created_at:new Date(Date.now()-15*86400000).toISOString()}
];
const state = process.argv.indexOf("--state");
if (state >= 0) items = items.filter(x => x.state === process.argv[state+1]);
const counts = mode === "empty" || mode === "missing-count" ? {} : mode === "invalid" ? {promoted:"unknown"} : {promoted:mode === "undercount" ? 1 : 17,parked:4,proposed:2};
console.log(JSON.stringify({generated_at:new Date().toISOString(),items,counts:{by_state:counts}}));
`);
await chmod(bin, 0o755);
process.env.COMPOUND_BIN = bin;
process.env.COMPOUND_HOME = join(scratch, "home");
process.env.COMPOUND_CONSOLE_MUTATIONS = "0";
process.env.COMPOUND_CONSOLE_OPERATOR_SESSION = "0";
// Disk cache is fixture-owned, never the operator console cache.
process.env.TMPDIR = scratch;
await Bun.spawn([bin,"version"],{stdout:"ignore",stderr:"ignore"}).exited;

const originalNow = Date.now;
let clockOffset = 0;
Date.now = () => originalNow() + clockOffset;
// Today never waits for compound: its first render of a value shows it as
// being read, and the read lands in the background. Settling joins every
// queued and running read (not only those in flight at call time).
const joinRefreshes = async () => {
  await Promise.all([...(globalThis.__compoundConsoleRunJson?.inflight.values() ?? [])].map(x => x.promise));
  const compound = await import("../src/lib/compound.ts");
  await compound.settleProjectionReads();
};

try {
  const compound = await import("../src/lib/compound.ts");
  const memory = await import("../src/lib/memory-data.ts");
  const { default: Memory } = await import("../src/app/memory/page.tsx");
  const { default: Briefing } = await import("../src/app/page.tsx");
  // Supply the framework contexts a real Next render provides. No memory
  // loader, card, projection or page implementation is replaced.
  const router = Object.fromEntries(["push","replace","refresh","back","forward","prefetch"].map(k => [k,()=>{}]));
  const renderPage = async () => renderToStaticMarkup(
    createElement(AppRouterContext.Provider,{value:router},
      createElement(PathnameContext.Provider,{value:"/memory"},
        createElement(SearchParamsContext.Provider,{value:new URLSearchParams()},
          await Memory({searchParams:Promise.resolve({})})))));

  // The page never waits for compound: render, let the reads it queued land, render again.
  const renderPageSettled = async () => { await renderPage(); await joinRefreshes(); return renderPage(); };
  const visible = (html) => html.replace(/<[^>]+>/g, "").replace(/\s+/g, " ");
  const noFalseWarning = /Could not read|not a current confirmed read|must recover|Check connection and access/;

  const renderHomeNow = async () => renderToStaticMarkup(createElement(AppRouterContext.Provider,{value:router},
    createElement(PathnameContext.Provider,{value:"/"},
      createElement(SearchParamsContext.Provider,{value:new URLSearchParams()},
        await Briefing()))));
  // Render, let the background reads it queued land, render again.
  const renderHome = async () => { await renderHomeNow(); await joinRefreshes(); return renderHomeNow(); };

  process.env.MEMORY_READ_SMOKE_MODE = "fail";
  const failed = await memory.loadProposedMemorySplit();
  assert.equal(failed.read?.status, "unreachable", "cold failed memory read must not be indistinguishable from live empty");
  assert(failed.read.error, "failed read retains its error");
  const firstHome = await renderHomeNow();
  assert.match(firstHome, /Reading memory for the first time/, "a value never read is shown as being read, not as zero");
  assert.doesNotMatch(firstHome, /promoted · 0 awaiting · 0 parked/);
  const unavailableHome = await renderHome();
  assert.match(unavailableHome, /Memory unavailable/, "home must not turn failed memory reads into zero");
  assert.doesNotMatch(unavailableHome, /promoted · 0 awaiting · 0 parked/, "unknown counts must not become a zero summary");
  const coldPage = await renderPage();
  assert.match(coldPage, /Reading proposed memories for the first time/, "a memory read never landed is shown as being read");
  assert.doesNotMatch(coldPage, /No proposed memories or proposed rules wait for you|No new supporting evidence/, "a cold page claims no empty queue");
  await joinRefreshes();
  const unavailable = await renderPage();
  assert.match(unavailable, /Could not read proposed memories/, "actual memory page discloses failed reads");
  assert.doesNotMatch(unavailable, /Clean seam|No proposed memories or proposed rules wait for you|No new supporting evidence/, "failed page may not claim an empty queue or source set");
  assert.match(visible(unavailable), /Proposal records by state\s*unknown/, "unavailable counts are unknown, not zero");
  assert.match(visible(unavailable), /How many memories agents have is unknown/, "a failed memory count is unknown, never 0");
  assert.doesNotMatch(visible(unavailable), /Agents remember 0/);

  process.env.MEMORY_READ_SMOKE_MODE = "empty";
  // A failed read is due again only after max(25 s, 2 x its run time).
  clockOffset += 26000;
  const empty = await memory.loadProposedMemorySplit();
  assert.equal(empty.read.status,"live");
  const emptyHome = await renderHome();
  assert.match(emptyHome, /promoted · 0 awaiting · 0 parked/, "real empty memory remains zero");
  assert.doesNotMatch(emptyHome, /Memory unavailable|Last known memory/);
  const liveEmpty = await renderPageSettled();
  assert.match(liveEmpty, /No new supporting evidence has been recorded/);
  assert.match(liveEmpty, /No proposed memories or proposed rules wait for you/);
  assert.match(visible(liveEmpty), /Agents remember 0 accepted memories\./, "a real empty memory is zero");
  assert.doesNotMatch(liveEmpty, noFalseWarning);

  process.env.MEMORY_READ_SMOKE_MODE = "data";
  clockOffset += 26000;
  await memory.loadProposedMemorySplit();
  await joinRefreshes();
  const live = await memory.loadProposedMemorySplit();
  assert.equal(live.read.status,"live");
  assert.deepEqual(live.actionable.map(x=>x.id),["cand_action"]);
  assert.deepEqual(live.sourceEvidence.map(x=>x.id),["cand_source"]);
  const livePage = await renderPageSettled();
  assert.match(livePage, /cand_action/, "the proposed memory renders");
  // KNOW-03: the headline counts memories (the canon report), not candidate
  // records, and names its unit; candidate states sit in a labelled disclosure.
  assert.match(visible(livePage), /Agents remember 23 accepted memories: 15 status notes · 5 decisions · 3 not sorted by kind\./);
  assert.doesNotMatch(visible(livePage), /Accepted for recall|Kept out of recall/);
  assert.match(visible(livePage), /Proposal records by state\s*23 records/);
  assert.match(visible(livePage), /1 proposed rule and 1 proposed memory wait for your choice/, "the header names what it counts");
  assert.doesNotMatch(livePage, noFalseWarning);
  // KNOW-02: a good copy past the 25 s cache window, with the refresh not yet
  // landed, is the last good read, not a fault. The page keeps the same cards
  // and shows no warning (it used to flip to "not a current confirmed read"
  // between auto-refreshes and push the cards down).
  clockOffset += 26000;
  const betweenRefreshes = await renderPage();
  assert.match(betweenRefreshes, /cand_action/, "cards stay while a refresh runs");
  assert.doesNotMatch(betweenRefreshes, noFalseWarning, "no stale warning for a copy that is only past the cache window");
  await joinRefreshes();
  await renderHome();
  await joinRefreshes();
  const liveHome = await renderHome();
  assert.match(liveHome, />17</, "actual home receives global memory counts");
  assert.match(liveHome, /promotions vs previous 7 days/, "covered live window shows comparison");
  clockOffset += 26000;
  process.env.MEMORY_READ_SMOKE_MODE = "fail";
  const stale = await memory.loadProposedMemorySplit();
  assert.equal(stale.read.status,"stale");
    assert(stale.read.lastGoodAt);
  assert.deepEqual(stale.sourceEvidence.map(x=>x.id),["cand_source"]);
  const staleHome = await renderHome();
  assert.match(staleHome, /Last known memory/);
  assert.match(staleHome, />17</, "home retains last-known count");
  assert.equal((staleHome.match(/Last known memory/g) ?? []).length, 2, "both home memory surfaces label stale totals");
  assert.match(staleHome, /Last successful read:/);
  assert.doesNotMatch(staleHome, /Memory unavailable|promotions vs previous 7 days/);
  const stalePage = await renderPageSettled();
  assert.match(stalePage,/Could not read proposed memories\. Last good copy is from/,"actual page marks last-good memory data after a failed refresh");
  assert.match(stalePage,/cand_action/,"the last good cards stay");
  assert.doesNotMatch(stalePage,/Clean seam|No proposed memories or proposed rules wait for you|No new supporting evidence/);
  // KNOW-03/13: the page never loads every candidate record to count them;
  // the split comes from the filtered read's counts.
  const argvLog = (await Bun.file(join(scratch, "argv.log")).text()).split("\n");
  assert.ok(!argvLog.includes("memory candidates --json"), "the Memory page must not read every candidate record");
  // Join the failed background refresh before invalidating or cleaning up.
  await joinRefreshes();

  for (const mode of ["invalid", "missing-count", "undercount"]) {
    compound.invalidateRunJsonCache();
    process.env.MEMORY_READ_SMOKE_MODE = mode;
    const invalidHome = await renderHome();
    assert.match(invalidHome, /Memory report incomplete/, mode + " must not become a false zero");
    assert.doesNotMatch(invalidHome, /promoted · 0 awaiting · 0 parked/);
    await joinRefreshes();
  }
  compound.invalidateRunJsonCache();
  process.env.MEMORY_READ_SMOKE_MODE = "slow";
  clockOffset += 26000;
  await memory.loadProposedMemorySplit();
  await joinRefreshes();
  const slow = await memory.loadProposedMemorySplit();
  assert.equal(slow.read.status,"live","memory loader must use the existing bounded heavy-read budget, not 15s");
  assert.equal(slow.sourceEvidence.length,1);
  console.log("memory-read honesty ok: cold failure, live empty, retained stale data, actual page rendering and >15s read");
} finally {
  await joinRefreshes();
  Date.now = originalNow;
  await rm(scratch,{recursive:true,force:true});
}
