import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";

import { normalizeExecutorProjection } from "@/components/agents/executor-normalize";
import { normalizeRunAttempts } from "@/lib/run-attempts";
import { buildTraffic } from "@/lib/traffic";
import { parseWorkListParams } from "@/lib/links";
import { plainState } from "@/lib/plain-words";
import { buildWorkLensItems } from "@/lib/work-views";
import { WorkTableLens } from "./WorkTableLens";

export function runWorkLifecycleStatusSelfTest(): { ok: boolean; details: string[] } {
  const checks: { name: string; ok: boolean }[] = [];
  const check = (name: string, ok: boolean) => checks.push({ name, ok });
  // Deliberately disagree with each Work lifecycle. Terminal attempts remain
  // execution evidence; a running attempt must not make held/claimed work run.
  const fixtures = [
    { id: "work_held_fixture", title: "Held fixture", state: "held", run: "running" },
    { id: "work_claimed_fixture", title: "Claimed fixture", state: "claimed", run: "running" },
    { id: "work_verifying_fixture", title: "Verifying fixture", state: "verifying", run: "succeeded" },
    { id: "work_running_fixture", title: "Running fixture", state: "running", run: "failed" },
  ];
  // A fixed clock: the running fixture was picked up 10 days before it (stale),
  // the others have no pick-up time (age unknown, so never stale).
  const NOW = Date.parse("2030-01-10T00:00:00Z");
  const pickedUp = (days: number) => String(BigInt(NOW - days * 86_400_000) * 1_000_000n);
  const open = fixtures.map(({ id, title, state }) => ({
    id, title, state, owner: "fixture-actor", next_action: "Follow the work lifecycle.",
    ...(state === "running" ? { claimed_at: pickedUp(10) } : {}),
  }));
  const projection = normalizeExecutorProjection({
    executors: [{
      id: "fixture-lane",
      label: "Fixture lane",
      status: "available",
      current_work: open.map((work) => ({ ...work, work_id: work.id })),
    }],
  });
  const attempts = normalizeRunAttempts(fixtures.map((fixture, index) => ({
    id: "ratt_fixture_" + index,
    work_id: fixture.id,
    status: fixture.run,
    harness: "codex",
    actor: "fixture-actor",
    created_at: "2026-01-01T00:00:00Z",
  })));
  const traffic = buildTraffic(projection, attempts);
  const items = buildWorkLensItems(open, traffic, { nowMs: NOW });
  const onOpenTrace = () => {};

  check("real join retains distinct work and attempt states", fixtures.every((fixture) => {
    const item = items.find((item) => item.workId === fixture.id);
    return item?.state === fixture.state && item?.runStatus === fixture.run;
  }));

  const list = (query: Record<string, string>, rows = items) =>
    renderToStaticMarkup(createElement(WorkTableLens, { items: rows, onOpenTrace, params: parseWorkListParams(query), groups: null }));
  const table = list({});
  const allTable = list({ filter: "all" });
  const blockedTable = list({ filter: "blocked" });
  const blockedBody = blockedTable.match(/<tbody>([\s\S]*?)<\/tbody>/)?.[1] ?? "";
  check("blocked-work link filters lifecycle rather than attempt status", blockedBody.includes("work_held_fixture") && !blockedBody.includes("work_running_fixture") && !blockedBody.includes("work_verifying_fixture"));
  const invalidFilterTable = list({ filter: "made-up" });
  check("invalid filter falls back to useful focus view", invalidFilterTable === table);
  const focusBody = table.match(/<tbody>([\s\S]*?)<\/tbody>/)?.[1] ?? "";
  const focusRows = focusBody.match(/<tr\b[^>]*>[\s\S]*?<\/tr>/g) ?? [];
  check("default focus is on hold or not moved in a week: held and the stale running item only", focusRows.length === 2
    && focusBody.includes("work_held_fixture") && focusBody.includes("work_running_fixture")
    && !focusBody.includes("work_claimed_fixture") && !focusBody.includes("work_verifying_fixture"));
  const body = allTable.match(/<tbody>([\s\S]*?)<\/tbody>/)?.[1] ?? "";
  const rows = body.match(/<tr\b[^>]*>[\s\S]*?<\/tr>/g) ?? [];
  check("table preserves held then not moved then the rest priority",
    body.indexOf("work_held_fixture") >= 0
    && body.indexOf("work_held_fixture") < body.indexOf("work_running_fixture")
    && body.indexOf("work_running_fixture") < body.indexOf("work_verifying_fixture"));
  for (const fixture of fixtures.filter((fixture) => fixture.state !== "claimed")) {
    const row = rows.find((row) => row.includes(fixture.id)) ?? "";
    const cells = row.match(/<td\b[^>]*>[\s\S]*?<\/td>/g) ?? [];
    const accent = fixture.state === "held" ? "seam" : fixture.state === "running" ? "crit" : "memory";
    check("table lifecycle badge and accent: " + fixture.state,
      (cells[2] ?? "").includes(">" + plainState(fixture.state) + "<")
      && !(cells[2] ?? "").includes(">" + fixture.run + "<")
      && row.includes("--row-accent:var(--" + accent + "-fg)"));
    check("table labels attempt separately: " + fixture.state,
      (cells[3] ?? "").includes("Run status: " + fixture.run));
  }

  const noAttemptItems = buildWorkLensItems(open, buildTraffic(projection, null), { nowMs: NOW });
  const noAttemptTable = list({ filter: "all" }, noAttemptItems);
  check("unavailable attempts retain lifecycle without fabricated attempt detail",
    noAttemptTable.includes("work_verifying_fixture") && !noAttemptTable.includes("Run status:"));
  return {
    ok: checks.every((check) => check.ok),
    details: checks.map((check) => (check.ok ? "ok" : "FAIL") + ": " + check.name),
  };
}
