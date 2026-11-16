import { NextResponse } from "next/server";

import { runCompound, str } from "@/lib/compound";
import { plainState } from "@/lib/plain-words";
import { findWorkbenchItem } from "@/lib/work-data";
import { isWorkId } from "@/lib/work-id";
import { buildActionRow } from "@/lib/work-pipeline";
import { workDates, workHistory, plainDay } from "@/lib/work-history";
import { workNextStep } from "@/lib/work-next-step";
import { mapEnvelopeSpine, parseWorkShow } from "@/lib/work-trace";

/*
 * Read-only projection behind the Trace drawer. GET only — no mutation ever
 * runs here (the drawer's action row posts to /api/decision/execute, the one
 * governed mutation endpoint). Built fresh from `compound work show <id>
 * --json` (envelope spine) + the workbench item (receipt_id, which work show
 * does not carry). The contextual state-transition options come from the
 * EXISTING work-advance state machine (WORK_TRANSITIONS / workOptionsFor) so
 * the drawer and the CLI can never disagree about what moves are legal.
 */

export const dynamic = "force-dynamic";

export async function GET(request: Request) {
  const id = new URL(request.url).searchParams.get("id")?.trim() ?? "";
  if (!isWorkId(id)) {
    return NextResponse.json({ error: "invalid work id" }, { status: 400 });
  }

  let raw: string;
  try {
    const result = await runCompound(["work", "show", id, "--json"]);
    raw = result.stdout;
  } catch {
    // The tool's own error text can carry folder paths; the drawer gets a sentence.
    return NextResponse.json(
      { error: "This work item could not be read. It may not be recorded yet." },
      { status: 404 },
    );
  }

  const { projection, lifecycle } = parseWorkShow(raw);
  if (!projection) {
    return NextResponse.json({ error: `could not parse projection for ${id}` }, { status: 500 });
  }

  const workbenchItem = await findWorkbenchItem(id);
  const receiptId = str(workbenchItem?.receipt_id);

  // History comes from the JSON `lifecycle` list; `--json` prints no text trailer.
  const history = workHistory(projection);
  const historyLines = lifecycle.length
    ? lifecycle
    : [...history].reverse().map((entry) => [entry.atMs !== null ? plainDay(entry.atMs) : "", entry.what, entry.who, entry.note].filter(Boolean).join(" · "));
  const trace = mapEnvelopeSpine(projection, historyLines, { receiptId });
  const nowMs = Date.now();

  // The short preview leads (the same next step and dates as the item page);
  // the technical record and the copy-only moves sit behind a disclosure.
  return NextResponse.json({
    generatedAt: new Date(nowMs).toISOString(),
    nowMs,
    preview: {
      stateLabel: plainState(trace.state || "unknown"),
      nextStep: workNextStep({ ...projection, claimed_by: str(workbenchItem?.claimed_by) || str(projection.claimed_by) }),
      dates: workDates(projection, { nowMs, claimedAt: workbenchItem?.claimed_at }),
    },
    trace,
    actionRow: buildActionRow(id, trace.state),
  });
}
