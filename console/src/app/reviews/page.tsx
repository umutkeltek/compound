import { CopyCmd } from "@/components/CopyCmd";
import { DecisionCard } from "@/components/DecisionCard";
import { DecisionDetailsDialog } from "@/components/DecisionDetailsDialog";
import { PanelApplyButton } from "@/components/PanelApplyButton";
import { Card, Chip, EmptyState, FreshnessChip } from "@/components/ui";
import { ago, arr, getReviews, num, reviewCommand, str, toDate, type Json } from "@/lib/compound";
import { consoleMutationsEnabled } from "@/lib/console-mutations";
import type { DecisionFormSpec, PlainLanguage } from "@/lib/decision-registry";
import { areaPlain } from "@/lib/glossary";
import { loadReviewCard, type ReviewCardData } from "@/lib/review-detail";
import { externalReviewRows } from "@/lib/review-data";

export const dynamic = "force-dynamic";

const EXTERNAL_AREA = "external-repo-intake";
const RUN_ID_RE = /^prun_[A-Za-z0-9_]+$/;

type Band = "needs_you" | "watching" | "filed";

type LoadedGroup = {
  group: Json;
  sample: Json;
  card: ReviewCardData | null;
};

/* ---------- band + grouping (§1/§3/§5: band drives everything below) ---------- */

function areaOf(row: Json): string {
  return str(row.area) || str(row.review_area);
}

function bandOf(row: Json): Band {
  const b = str(row.band);
  return b === "watching" || b === "filed" ? b : "needs_you";
}

function buildItemIndex(reviews: Json | null): Map<string, Json> {
  const map = new Map<string, Json>();
  for (const item of arr(reviews?.items)) {
    const id = str(item.id) || str(item.review_id);
    if (id) map.set(id, item);
  }
  return map;
}

function uniq(values: string[]): string[] {
  return [...new Set(values.filter(Boolean))].sort();
}

/**
 * Deterministic local re-grouping key for items the Go projection left as
 * singletons (`summary_source: "fallback"` writer debt — see design spec §5
 * and §10). Prefers the item's own group_key so items that genuinely share
 * one (e.g. every contradiction from one synthesis sweep) still collapse
 * into a single card client-side instead of vanishing from Needs You.
 */
function fallbackGroupKey(row: Json): string {
  const area = areaOf(row) || "review";
  const basis =
    str(row.group_key) ||
    str(row.context_label) ||
    str(row.operator_summary) ||
    str(row.next_action) ||
    str(row.id) ||
    "decision";
  return `${area}:${basis.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "").slice(0, 80)}`;
}

function fallbackGroups(rows: Json[]): Json[] {
  const byKey = new Map<string, Json>();
  for (const row of rows) {
    const key = fallbackGroupKey(row);
    const previous = byKey.get(key);
    const evidence = uniq([...(arr(previous?.evidence_refs).map(String) ?? []), ...arr(row.evidence_refs).map(String)]);
    const itemIds = uniq([...(arr(previous?.item_ids).map(String) ?? []), str(row.id) || str(row.review_id)]);
    byKey.set(key, {
      ...(previous ?? {}),
      key,
      area: areaOf(row),
      band: bandOf(row),
      count: num(previous?.count) + 1,
      status: str(previous?.status) || str(row.status),
      readiness: str(previous?.readiness) || str(row.readiness),
      operator_summary:
        str(previous?.operator_summary) ||
        str(row.operator_summary) ||
        str(row.next_action) ||
        str(row.id) ||
        "Review this decision.",
      operator_unlock: str(previous?.operator_unlock) || str(row.operator_unlock),
      context_label: str(previous?.context_label) || str(row.context_label) || areaOf(row),
      item_ids: itemIds,
      evidence_refs: evidence,
      terminal_reason: str(previous?.terminal_reason) || "owner has not exposed a safe action for this decision group",
      sample: previous?.sample ?? row,
    });
  }
  return [...byKey.values()];
}

/**
 * Backend groups for one band, plus a client-side fallback grouping of any
 * band-matching items the Go projection left ungrouped — so nothing that
 * needs a decision silently drops off the page just because its area's
 * grouping isn't wired up on the backend yet.
 */
function groupsForBand(reviews: Json | null, band: "needs_you" | "watching"): Json[] {
  const backend = arr(reviews?.groups).filter((g: Json) => bandOf(g) === band && areaOf(g) !== EXTERNAL_AREA);
  const covered = new Set(backend.flatMap((g) => arr(g.item_ids).map(String)));
  const leftover = arr(reviews?.items).filter((it: Json) => {
    if (bandOf(it) !== band) return false;
    if (areaOf(it) === EXTERNAL_AREA) return false;
    const id = str(it.id) || str(it.review_id);
    return Boolean(id) && !covered.has(id);
  });
  return [...backend, ...fallbackGroups(leftover)];
}

function groupCreatedAt(group: Json, itemByID: Map<string, Json>): string {
  const sample = group.sample;
  const sampleCreated = sample && typeof sample === "object" ? str((sample as Json).created_at) : "";
  if (sampleCreated) return sampleCreated;
  let min = "";
  for (const id of arr(group.item_ids).map(String)) {
    const created = str(itemByID.get(id)?.created_at);
    if (created && (!min || created < min)) min = created;
  }
  return min || str(group.created_at);
}

function isOldAge(createdAt: string): boolean {
  const d = toDate(createdAt);
  if (!d) return false;
  return Date.now() - d.getTime() > 48 * 3600 * 1000;
}

function sampleForGroup(group: Json, itemByID: Map<string, Json>): Json {
  const sample = group.sample;
  if (sample && typeof sample === "object") return sample as Json;
  for (const id of arr(group.item_ids).map(String)) {
    const item = itemByID.get(id);
    if (item) return item;
  }
  return {
    id: str(group.key),
    area: areaOf(group),
    readiness: str(group.readiness),
    status: str(group.status),
    next_action: str(group.operator_summary),
    evidence_refs: arr(group.evidence_refs),
  };
}

async function loadGroups(groups: Json[], itemByID: Map<string, Json>): Promise<LoadedGroup[]> {
  return Promise.all(
    groups.map(async (group) => {
      const sample = sampleForGroup(group, itemByID);
      return { group, sample, card: sample ? await loadReviewCard(sample) : null };
    }),
  );
}

function sampleClaim(sample: Json): string {
  const card = sample.decision_card;
  return card && typeof card === "object" ? str((card as Json).claim) : "";
}

/** Prefer the backend's evidence_summaries labels (readable) over raw refs;
 * fall back to raw refs when the backend hasn't populated summaries yet. */
function groupEvidence(group: Json): string[] {
  const summaries = arr(group.evidence_summaries).filter(
    (s): s is Json => Boolean(s) && typeof s === "object",
  );
  if (summaries.length) {
    return summaries.map((s) => str(s.label) || str(s.ref)).filter(Boolean).slice(0, 12);
  }
  return arr(group.evidence_refs).map(String).filter(Boolean).slice(0, 12);
}

function sortByAge(rows: Json[], itemByID: Map<string, Json>): Json[] {
  return rows
    .slice()
    .sort((a, b) => (groupCreatedAt(a, itemByID) || "9999").localeCompare(groupCreatedAt(b, itemByID) || "9999"));
}

/* ---------- change 2: real verb buttons for safe batch acts ---------- */

function safeBatchAction(group: Json): Json | null {
  const action = group.safe_batch_action;
  return action && typeof action === "object" ? (action as Json) : null;
}

function actionArgv(action: Json): string[] {
  const argv = arr(action.argv).map(String).filter(Boolean);
  return argv[0] === "compound" ? argv.slice(1) : argv;
}

function sampleRunId(sample: Json): string {
  const card = sample.decision_card;
  if (!card || typeof card !== "object") return "";
  const meta = (card as Json).meta;
  if (!meta || typeof meta !== "object") return "";
  return str((meta as Json).run_id);
}

function batchRunId(action: Json, group: Json, sample: Json): string {
  const argv = arr(action.argv).map(String);
  const flagIndex = argv.indexOf("--plan");
  if (flagIndex >= 0 && RUN_ID_RE.test(argv[flagIndex + 1] ?? "")) return argv[flagIndex + 1];
  const legacyFlagIndex = argv.indexOf("--run");
  if (legacyFlagIndex >= 0 && RUN_ID_RE.test(argv[legacyFlagIndex + 1] ?? "")) return argv[legacyFlagIndex + 1];
  const keyed = str(group.key).match(/\|(prun_[A-Za-z0-9_]+)\|/)?.[1];
  if (keyed) return keyed;
  const fromSample = sampleRunId(sample);
  return RUN_ID_RE.test(fromSample) ? fromSample : "";
}

function batchActionLabel(group: Json, action: Json): string {
  const label = str(action.label);
  if (label) return label;
  const count = num(group.count) || arr(group.item_ids).length || 1;
  if (areaOf(group) === "panel-verdicts") return `Record ${count} verdict${count === 1 ? "" : "s"}`;
  return `Apply ${count} item${count === 1 ? "" : "s"}`;
}

/** What CopyCmd shows when there is no one-click act (or mutations are gated
 * off): the backend's own command string/argv when it supplied one, else the
 * generic area review command. */
function fallbackCommand(group: Json, sample: Json): string {
  const action = safeBatchAction(group);
  if (action) {
    const command = str(action.command);
    if (command) return command;
    const argv = actionArgv(action);
    if (argv.length) return `compound ${argv.join(" ")}`;
  }
  return reviewCommand(sample);
}

function GroupActionArea({ group, sample }: { group: Json; sample: Json }) {
  const action = safeBatchAction(group);
  const cmd = fallbackCommand(group, sample);

  if (!action || arr(action.argv).length === 0) {
    return (
      <div className="flex flex-wrap items-center gap-2">
        <CopyCmd cmd={cmd} compact />
        {str(group.terminal_reason) ? (
          <span className="text-[10.5px] text-ink-3">{str(group.terminal_reason)}</span>
        ) : null}
      </div>
    );
  }

  const runId = batchRunId(action, group, sample);
  if (!runId) {
    return (
      <div className="flex flex-wrap items-center gap-2">
        <CopyCmd cmd={cmd} compact />
        <span className="text-[10.5px] text-ink-3">
          {str(group.terminal_reason) || "batch command exposed without a recognizable run id"}
        </span>
      </div>
    );
  }

  if (!consoleMutationsEnabled()) {
    return (
      <div className="flex flex-wrap items-center gap-2">
        <CopyCmd cmd={cmd} compact />
        <Chip tone="gray">terminal-only for now</Chip>
      </div>
    );
  }

  return (
    <PanelApplyButton runId={runId} label={batchActionLabel(group, action)} argv={actionArgv(action)} />
  );
}

/* ---------- change 1 + 3: the group card ---------- */

function GroupCard({ loaded, itemByID }: { loaded: LoadedGroup; itemByID: Map<string, Json> }) {
  const { group, sample, card } = loaded;
  const summary = str(group.operator_summary) || card?.headline || "Review this decision.";
  const unlock = str(group.operator_unlock);
  const area = areaOf(group);
  const context = str(group.context_label);
  const count = num(group.count) || arr(group.item_ids).length || 1;
  const createdAt = groupCreatedAt(group, itemByID);
  const old = isOldAge(createdAt);
  const itemIds = arr(group.item_ids).map(String);
  const sampleId = str(sample.id) || str(group.key);

  const pl: PlainLanguage = {
    headline: summary,
    body: sampleClaim(sample) || card?.body || "",
    evidence: groupEvidence(group),
  };
  const formSpec: DecisionFormSpec = { area: area || "review", options: [], fields: [] };

  return (
    <div className="rounded-xl border border-line bg-white px-4 py-3.5">
      <div className="min-w-0">
        <div className="text-[15.5px] font-semibold leading-snug text-ink">{summary}</div>
        {unlock ? <div className="mt-1 text-[13px] font-medium text-emerald-700">&rarr; {unlock}</div> : null}
      </div>

      <div className="mt-2 flex flex-wrap items-center gap-1.5">
        {area ? (
          <Chip tone="gray" title={areaPlain(area)}>
            {area.replace(/-/g, " ")}
          </Chip>
        ) : null}
        {context ? <Chip tone="violet">{context}</Chip> : null}
        {count > 1 ? <span className="text-[11px] text-ink-3">+{count - 1} more</span> : null}
      </div>

      <div className="mt-3 flex flex-wrap items-center justify-between gap-2">
        <span className={`text-[11px] ${old ? "font-semibold text-amber-700" : "text-ink-3"}`}>
          {createdAt ? `${ago(createdAt)}${old ? " ⚠" : ""}` : "age unknown"}
        </span>
        <div className="flex flex-wrap items-center gap-2">
          <DecisionDetailsDialog
            area={area || "review"}
            itemId={sampleId}
            item={sample}
            pl={pl}
            formSpec={formSpec}
            buttonLabel="Evidence"
          />
          <GroupActionArea group={group} sample={sample} />
        </div>
      </div>

      {itemIds.length > 1 ? (
        <details className="mt-3 rounded-lg border border-line bg-canvas px-2.5 py-2 text-[11px] text-ink-3">
          <summary className="cursor-pointer font-medium text-ink-2">Review each &middot; {itemIds.length} members</summary>
          <p className="mt-2 text-[10.5px] leading-relaxed text-ink-3">
            The act above is run-scoped &mdash; it applies to every member together; members cannot be excluded one by one.
          </p>
          <div className="mt-2 flex flex-col gap-1">
            {itemIds.slice(0, 60).map((id) => {
              const member = itemByID.get(id);
              const label = str(member?.context_label) || id;
              return (
                <div key={id} className="flex items-center justify-between gap-2 rounded-md bg-white px-2 py-1">
                  <span className="mono min-w-0 truncate text-[10.5px] text-ink-3" title={id}>
                    {label}
                  </span>
                  <CopyCmd cmd={id} compact />
                </div>
              );
            })}
          </div>
          {itemIds.length > 60 ? (
            <p className="mt-1 text-[10.5px] text-ink-3">+{itemIds.length - 60} more member ids not shown.</p>
          ) : null}
          <code className="mono mt-2 block truncate text-[9.5px] text-ink-3">
            {str(group.key)} &middot; {itemIds.length} members
          </code>
        </details>
      ) : (
        <code className="mono mt-2 block truncate text-[9.5px] text-ink-3">{sampleId}</code>
      )}
    </div>
  );
}

/* ---------- change 1: watching + filed bands ---------- */

function asWatchingEntry(row: Json): Json {
  return {
    ...row,
    count: num(row.count) || 1,
    item_ids: arr(row.item_ids).length ? row.item_ids : [str(row.id) || str(row.review_id)],
  };
}

function WatchingRow({ entry, itemByID }: { entry: Json; itemByID: Map<string, Json> }) {
  const summary = str(entry.operator_summary) || "Still watching.";
  const area = areaOf(entry);
  const count = num(entry.count) || arr(entry.item_ids).length || 1;
  const createdAt = groupCreatedAt(entry, itemByID);
  return (
    <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line px-3 py-2.5 last:border-b-0">
      <div className="flex min-w-0 items-center gap-2">
        {area ? (
          <span className="shrink-0 rounded-md bg-canvas px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-ink-3">
            {area.replace(/-/g, " ")}
          </span>
        ) : null}
        <span className="min-w-0 truncate text-[12.5px] text-ink-2">{summary}</span>
      </div>
      <div className="flex shrink-0 items-center gap-2">
        {count > 1 ? <Chip tone="gray">{count}</Chip> : null}
        <span className="text-[11px] text-ink-3">{createdAt ? ago(createdAt) : ""}</span>
      </div>
    </div>
  );
}

export default async function Reviews() {
  const reviews = await getReviews();
  const itemByID = buildItemIndex(reviews);

  const externalRows = externalReviewRows(reviews);
  const externalNeedsYou = externalRows.filter((r) => bandOf(r) !== "watching");
  const externalWatching = externalRows.filter((r) => bandOf(r) === "watching");

  const needsYouGroups = sortByAge(groupsForBand(reviews, "needs_you"), itemByID);
  const watchingEntries = sortByAge(
    [...groupsForBand(reviews, "watching"), ...externalWatching.map(asWatchingEntry)],
    itemByID,
  );

  const loadedNeedsYouGroups = await loadGroups(needsYouGroups, itemByID);

  const allItems = arr(reviews?.items);
  const filedItems = allItems.filter((it) => bandOf(it) === "filed");
  const filedTotal = filedItems.length;
  const filedByAreaMap = new Map<string, number>();
  for (const item of filedItems) {
    const area = areaOf(item) || "unclassified";
    filedByAreaMap.set(area, (filedByAreaMap.get(area) ?? 0) + 1);
  }
  const filedByArea = [...filedByAreaMap.entries()].sort((a, b) => b[1] - a[1]);

  const needsYouItemTotal =
    loadedNeedsYouGroups.reduce((sum, { group }) => sum + (num(group.count) || arr(group.item_ids).length || 1), 0) +
    externalNeedsYou.length;
  const needsYouCardTotal = loadedNeedsYouGroups.length + (externalNeedsYou.length ? 1 : 0);
  const needsYouEmpty = needsYouCardTotal === 0;

  return (
    <div className="flex flex-col gap-8">
      <div className="flex items-end justify-between gap-4">
        <div>
          <h1 className="text-[22px] font-semibold tracking-tight">Reviews</h1>
          <p className="mt-0.5 text-[12.5px] text-ink-3">
            {needsYouEmpty
              ? `Nothing waits on you. ${filedTotal} record${filedTotal === 1 ? "" : "s"} filed.`
              : `${needsYouItemTotal} item${needsYouItemTotal === 1 ? "" : "s"} need your decision in ${needsYouCardTotal} card${needsYouCardTotal === 1 ? "" : "s"}.`}
          </p>
        </div>
        <FreshnessChip generatedAt={str(reviews?.generated_at)} source="compound reviews --json" />
      </div>

      {/* NEEDS YOU — always first: this band renders before anything informational. */}
      <section className="flex flex-col gap-3">
        <div className="flex items-baseline justify-between gap-2">
          <h2 className="text-[14px] font-semibold text-ink">Needs you</h2>
          {!needsYouEmpty ? <Chip tone="orange">{needsYouItemTotal}</Chip> : null}
        </div>

        {needsYouEmpty ? (
          <EmptyState>Nothing waits on you. {filedTotal} records filed.</EmptyState>
        ) : (
          <>
            {externalNeedsYou.length ? (
              <Card
                title="Shared repos awaiting your call"
                accent="blue"
                glyph="E"
                right={<Chip tone="orange">{externalNeedsYou.length} open</Chip>}
              >
                <p className="mb-3 text-[11.5px] text-ink-3">{areaPlain(EXTERNAL_AREA)}</p>
                <div className="grid gap-3 lg:grid-cols-2">
                  {externalNeedsYou.map((row) => (
                    <DecisionCard key={str(row.id)} area={EXTERNAL_AREA} item={row} />
                  ))}
                </div>
              </Card>
            ) : null}

            {loadedNeedsYouGroups.length ? (
              <div className="grid gap-3 lg:grid-cols-2">
                {loadedNeedsYouGroups.map((loaded) => (
                  <GroupCard key={str(loaded.group.key)} loaded={loaded} itemByID={itemByID} />
                ))}
              </div>
            ) : null}
          </>
        )}
      </section>

      {/* WATCHING — informational holds only: no buttons, no commands. */}
      {watchingEntries.length ? (
        <section className="flex flex-col gap-3">
          <div className="flex items-baseline justify-between gap-2">
            <h2 className="text-[14px] font-semibold text-ink">Watching</h2>
            <Chip tone="gray">{watchingEntries.length}</Chip>
          </div>
          <p className="text-[11.5px] text-ink-3">Informational holds &mdash; nothing to decide yet, no buttons on purpose.</p>
          <div className="card overflow-hidden">
            {watchingEntries.map((entry, index) => (
              <WatchingRow key={str(entry.key) || str(entry.id) || index} entry={entry} itemByID={itemByID} />
            ))}
          </div>
        </section>
      ) : null}

      {/* FILED — one calm strip, never actionable. */}
      <section>
        <div className="mb-2 flex items-baseline justify-between gap-2">
          <h2 className="text-[13px] font-semibold text-ink-2">Filed</h2>
          <span className="text-[11.5px] text-ink-3">
            {filedTotal} record{filedTotal === 1 ? "" : "s"} filed
          </span>
        </div>
        {filedByArea.length ? (
          <div className="flex flex-wrap gap-2">
            {filedByArea.map(([area, count]) => (
              <span
                key={area}
                title={areaPlain(area)}
                className="rounded-lg border border-line bg-white px-2.5 py-1 text-[11px] text-ink-3"
              >
                {area.replace(/-/g, " ")} &middot; {count}
              </span>
            ))}
          </div>
        ) : (
          <p className="text-[11.5px] text-ink-3">Nothing filed yet.</p>
        )}
      </section>
    </div>
  );
}
