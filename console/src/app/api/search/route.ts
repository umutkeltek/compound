import { NextResponse } from "next/server";

import { readAttention } from "@/lib/attention";
import { readProjection, readWorkProjectIndex, WORK_PROJECT_INDEX_READS } from "@/lib/compound";
import { buildSearchIndex } from "./build";

export const dynamic = "force-dynamic";

/**
 * Search's index of apps, waiting decisions and open work. Read-only and
 * non-blocking: every read is the cached copy the pages already use (the
 * sidebar's app list, Today and Decide's attention read, Work's workbench
 * read and its by-app grouping), or "loading" while the first read runs. The
 * palette asks once when it opens and filters in the browser, so typing never
 * starts a read.
 */
export async function GET() {
  const { work, apps } = WORK_PROJECT_INDEX_READS;
  const [attention, growthRead, workRead, projects] = await Promise.all([
    readAttention(),
    readProjection([...apps.args], { timeoutMs: apps.timeoutMs }),
    readProjection([...work.args], { timeoutMs: work.timeoutMs }),
    readWorkProjectIndex(),
  ]);
  const index = buildSearchIndex({
    growth: { status: growthRead.status, value: growthRead.value },
    attention: { status: attention.read.status, report: attention.report },
    work: { status: workRead.status, value: workRead.value },
    projects: projects.index,
  });
  return NextResponse.json(index, { headers: { "cache-control": "no-store" } });
}
