import { HeaderStat, hasCopy, PageHeader, ReadChip, ReadFailedBanner, ReadNotice, readsPending, aboutDuration } from "@/components/kit";
import { CopyActionButton } from "@/components/CopyCmd";
import { PendingRefresh } from "@/components/PendingRefresh";
import { WorkLenses } from "@/components/work/WorkLenses";
import { readAttention, waitingOnYou } from "@/lib/attention";
import { arr, DIRECTOR_PROPOSE_ARGS, HEAVY_PROJECTION_TIMEOUT_MS, readProjection, str } from "@/lib/compound";
import { consoleMutationsEnabled } from "@/lib/console-mutations";
import { computeConsoleMutationReadiness } from "@/lib/instance-pin";
import { buildLaneTraffic } from "@/lib/lane-traffic";
import { getTraffic } from "@/lib/traffic";
import { isWorkId } from "@/lib/work-id";
import { buildBoard, buildOffered, buildPipeline } from "@/lib/work-pipeline";
import { buildWorkLensItems, groupWorkLensItems } from "@/lib/work-views";
import type { WorkLensKey } from "@/components/work/WorkLenses";

export const dynamic = "force-dynamic";

/**
 * Work — "Where is every unit of work in its lifecycle, and what can I move?"
 * The default Board lens answers who owns each active item and what moves next.
 * Every read is non-blocking (`readProjection`): the page renders at once from
 * cached copies, or says what is still being read; it never waits for compound.
 */
export default async function WorkPage({
  searchParams,
}: {
  searchParams: Promise<{ trace?: string; lens?: string }>;
}) {
  const { trace, lens } = await searchParams;

  const [workbenchRead, sourcesRead, suggestionsRead, packetsRead, laneTraffic, traffic, attentionLive] = await Promise.all([
    readProjection(["workbench", "--json"], { timeoutMs: HEAVY_PROJECTION_TIMEOUT_MS }),
    readProjection(["source", "connections", "--json"]),
    // Read-only: `director propose --dry-run` creates nothing; suggestions feed
    // the Suggest stage. It takes about two minutes on a real home, so it runs
    // under the heavy timeout (the old 15 s limit meant it never landed).
    readProjection(DIRECTOR_PROPOSE_ARGS, { timeoutMs: HEAVY_PROJECTION_TIMEOUT_MS }),
    readProjection(["packets", "list", "--json"]),
    buildLaneTraffic(200, { nonBlocking: true }),
    getTraffic(),
    readAttention(),
  ]);
  const workbench = workbenchRead.value;
  const suggestions = suggestionsRead.value;

  const open = arr(workbench?.open);
  const recent = arr(workbench?.recent);
  const proposals = arr(suggestions?.proposals ?? suggestions?.proposed ?? suggestions?.items);

  // A stage whose source has not been read says so, instead of "none".
  const stages = buildPipeline(workbench, sourcesRead.value, proposals).map((stage) => {
    if (stage.key === "signal" && !hasCopy(sourcesRead)) {
      return { ...stage, emptyCopy: sourcesRead.status === "unreachable" ? "Could not read sources." : "Reading sources…" };
    }
    if (stage.key === "propose" && !hasCopy(suggestionsRead)) {
      const how = suggestionsRead.lastRunMs ? ` (last time ${aboutDuration(suggestionsRead.lastRunMs)})` : "";
      return { ...stage, emptyCopy: suggestionsRead.status === "unreachable" ? "Could not read suggestions." : `Reading suggestions${how}…` };
    }
    return stage;
  });
  const columns = buildBoard(open, recent);
  const offered = buildOffered(arr(packetsRead.value?.items));

  const initialTrace = isWorkId(str(trace)) ? str(trace) : null;
  const mutationsReady = computeConsoleMutationReadiness(consoleMutationsEnabled()).ready;
  const items = buildWorkLensItems(open, traffic);
  const groups = groupWorkLensItems(items, traffic);
  const allowedLenses = new Set<WorkLensKey>(["pipeline", "table", "flow", "space", "board", "offered"]);
  const defaultLens = allowedLenses.has(lens as WorkLensKey) ? lens as WorkLensKey : "board";
  const count = (key: string) => columns.find((column) => column.key === key)?.cards.length ?? 0;
  const workbenchKnown = hasCopy(workbenchRead);
  // "Waiting on you" has ONE definition (lib/attention.ts waitingOnYou), shared
  // with the sidebar badge and Today. Work shows its work-item count.
  const waiting = waitingOnYou(attentionLive.report);
  const pending = readsPending(workbenchRead, sourcesRead, suggestionsRead, packetsRead, traffic.read, attentionLive.read);

  return (
    <div className="console-stage flex flex-col gap-6">
      <PendingRefresh pending={pending} stamp={Date.now()} />
      <PageHeader
        breadcrumb="operate / work"
        title="Work"
        stats={
          workbenchKnown ? (
            <>
              <HeaderStat value={open.length} label="open" />
              {waiting ? (
                <HeaderStat value={waiting.workItems} label={waiting.workItems === 1 ? "work item waits on you" : "work items wait on you"} tone={waiting.workItems ? "seam" : undefined} />
              ) : null}
              <HeaderStat value={count("running")} label="in progress" />
              <HeaderStat value={count("review")} label="in review" />
              <HeaderStat value={count("ready")} label="ready" />
            </>
          ) : null
        }
        right={
          <>
            <ReadChip read={workbenchRead} />
            <CopyActionButton cmd={'compound work create --owner <owner> --predicate "<what done looks like>"'} label="New work" />
          </>
        }
      />

      {workbenchKnown ? (
        <>
          <ReadFailedBanner read={workbenchRead} label="work" />
          <WorkLenses
            stages={stages}
            columns={columns}
            offered={offered}
            openCount={open.length}
            initialTrace={initialTrace}
            laneTraffic={laneTraffic}
            items={items}
            groups={groups}
            defaultLens={defaultLens}
            mutationsReady={mutationsReady}
          />
        </>
      ) : (
        <ReadNotice read={workbenchRead} label="work" />
      )}
    </div>
  );
}
