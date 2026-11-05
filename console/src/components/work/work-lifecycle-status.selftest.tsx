import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";

import { normalizeExecutorProjection } from "@/components/agents/executor-normalize";
import { normalizeRunAttempts } from "@/lib/run-attempts";
import { buildTraffic } from "@/lib/traffic";
import { buildPipeline } from "@/lib/work-pipeline";
import { buildWorkLensItems, groupWorkLensItems } from "@/lib/work-views";
import { WorkFlowLens } from "./WorkFlowLens";
import { WorkSpaceLens } from "./WorkSpaceLens";
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
  const open = fixtures.map(({ id, title, state }) => ({
    id, title, state, owner: "fixture-actor", next_action: "Follow the work lifecycle.",
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
  const items = buildWorkLensItems(open, traffic);
  const groups = groupWorkLensItems(items, traffic);
  const stages = buildPipeline({ open, recent: [] }, null, []);
  const onOpenTrace = () => {};

  check("real join retains distinct work and attempt states", fixtures.every((fixture) => {
    const item = items.find((item) => item.workId === fixture.id);
    return item?.state === fixture.state && item?.runStatus === fixture.run;
  }));

  const table = renderToStaticMarkup(createElement(WorkTableLens, { items, onOpenTrace }));
  const body = table.match(/<tbody>([\s\S]*?)<\/tbody>/)?.[1] ?? "";
  const rows = body.match(/<tr\b[^>]*>[\s\S]*?<\/tr>/g) ?? [];
  check("default focus keeps held, running and verifying work but excludes claimed", rows.length === 3
    && !body.includes("work_claimed_fixture")
    && ["held", "running", "verifying"].every((state) => body.includes("work_" + state + "_fixture")));
  check("table preserves held then running then verifying priority",
    body.indexOf("work_held_fixture") >= 0
    && body.indexOf("work_held_fixture") < body.indexOf("work_running_fixture")
    && body.indexOf("work_running_fixture") < body.indexOf("work_verifying_fixture"));
  for (const fixture of fixtures.filter((fixture) => fixture.state !== "claimed")) {
    const row = rows.find((row) => row.includes(fixture.id)) ?? "";
    const cells = row.match(/<td\b[^>]*>[\s\S]*?<\/td>/g) ?? [];
    const accent = fixture.state === "held" ? "seam" : fixture.state === "running" ? "work" : "memory";
    check("table lifecycle badge and accent: " + fixture.state,
      (cells[2] ?? "").includes(">" + fixture.state + "<")
      && !(cells[2] ?? "").includes(">" + fixture.run + "<")
      && row.includes("--row-accent:var(--" + accent + "-fg)"));
    check("table labels attempt separately: " + fixture.state,
      (cells[1] ?? "").includes("Run status: " + fixture.run));
  }

  const flow = renderToStaticMarkup(createElement(WorkFlowLens, { stages, groups, onOpenTrace }));
  check("flow's three-card limit prioritizes work lifecycle",
    !flow.includes("work_claimed_fixture")
    && flow.indexOf("work_running_fixture") >= 0
    && flow.indexOf("work_running_fixture") < flow.indexOf("work_verifying_fixture")
    && flow.indexOf("work_verifying_fixture") < flow.indexOf("work_held_fixture"));
  for (const fixture of fixtures.filter((fixture) => fixture.state !== "claimed")) {
    const card = (flow.match(/<button\b[^>]*>[\s\S]*?<\/button>/g) ?? [])
      .find((card) => card.includes(fixture.id)) ?? "";
    check("flow lifecycle badge and separate attempt: " + fixture.state,
      card.includes(">" + fixture.state + "<") && card.includes("Run status: " + fixture.run));
  }

  const space = renderToStaticMarkup(createElement(WorkSpaceLens, { stages, groups, onOpenTrace }));
  check("space's three-card limit prioritizes work lifecycle",
    !space.includes("Claimed fixture")
    && space.includes('aria-label="Running fixture, running,')
    && space.includes('aria-label="Verifying fixture, verifying,')
    && space.includes('aria-label="Held fixture, held,'));
  const inspector = space.match(/<aside\b[^>]*>([\s\S]*?)<\/aside>/)?.[1] ?? "";
  check("space inspector keeps running work primary and failed attempt separate",
    inspector.includes("work_running_fixture") && inspector.includes(">running<")
    && inspector.includes("Run status: failed") && !inspector.includes(">failed<"));
  const noAttemptItems = buildWorkLensItems(open, buildTraffic(projection, null));
  const noAttemptTable = renderToStaticMarkup(createElement(WorkTableLens, { items: noAttemptItems, onOpenTrace }));
  check("unavailable attempts retain lifecycle without fabricated attempt detail",
    noAttemptTable.includes("work_verifying_fixture") && !noAttemptTable.includes("Run status:"));
  return {
    ok: checks.every((check) => check.ok),
    details: checks.map((check) => (check.ok ? "ok" : "FAIL") + ": " + check.name),
  };
}
