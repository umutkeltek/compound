import { CopyCmd } from "@/components/CopyCmd";
import { DecisionCard } from "@/components/DecisionCard";
import { Card, Chip, EmptyState, FreshnessChip } from "@/components/ui";
import { ago, arr, getReviews, num, reviewCommand, str, type Json } from "@/lib/compound";
import { areaPlain } from "@/lib/glossary";
import { loadReviewCard, type ReviewCardData } from "@/lib/review-detail";
import { externalReviewRows } from "@/lib/review-data";

export const dynamic = "force-dynamic";

const NEEDS_DECISION = new Set(["review_required", "needs_review", "unknown", "open", ""]);
const GROUP_CAP = 8;

type LoadedGroup = {
  group: Json;
  sample: Json;
  card: ReviewCardData | null;
};

function areaOf(row: Json): string {
  return str(row.area) || str(row.review_area);
}

function pendingReviewRows(reviews: Json | null): Json[] {
  return arr(reviews?.items).filter((row: Json) => {
    const area = areaOf(row);
    if (!area || area === "external-repo-intake") return false;
    return NEEDS_DECISION.has(str(row.readiness)) || NEEDS_DECISION.has(str(row.status));
  });
}

function uniq(values: string[]): string[] {
  return [...new Set(values.filter(Boolean))].sort();
}

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
      count: num(previous?.count) + 1,
      status: str(previous?.status) || str(row.status),
      readiness: str(previous?.readiness) || str(row.readiness),
      band: str(previous?.band) || str(row.band),
      operator_summary:
        str(previous?.operator_summary) ||
        str(row.operator_summary) ||
        str(row.next_action) ||
        str(row.id) ||
        "Review this decision.",
      context_label: str(previous?.context_label) || str(row.context_label) || areaOf(row),
      item_ids: itemIds,
      evidence_refs: evidence,
      terminal_reason: "installed compound does not expose a grouped batch action for this decision yet",
      sample: previous?.sample ?? row,
    });
  }
  return [...byKey.values()];
}

function groupRows(reviews: Json | null): Json[] {
  const projected = arr(reviews?.groups).filter((group: Json) => areaOf(group) !== "external-repo-intake");
  if (projected.length) return projected;
  return fallbackGroups(pendingReviewRows(reviews));
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

async function loadGroups(groups: Json[], reviews: Json | null): Promise<LoadedGroup[]> {
  const itemByID = new Map<string, Json>();
  for (const item of arr(reviews?.items)) {
    const id = str(item.id) || str(item.review_id);
    if (id) itemByID.set(id, item);
  }
  return Promise.all(
    groups.map(async (group) => {
      const sample = sampleForGroup(group, itemByID);
      return {
        group,
        sample,
        card: sample ? await loadReviewCard(sample) : null,
      };
    }),
  );
}

function actionCommand(group: Json, sample: Json): string {
  const action = group.safe_batch_action;
  if (action && typeof action === "object") {
    const command = str((action as Json).command);
    if (command) return command;
    const argv = arr((action as Json).argv).map(String).filter(Boolean);
    if (argv.length) return argv.join(" ");
  }
  return reviewCommand(sample);
}

function chipText(value: string): string {
  return value.replace(/_/g, " ");
}

function EvidenceDrawer({ group }: { group: Json }) {
  const evidence = arr(group.evidence_refs).map(String).filter(Boolean);
  const items = arr(group.item_ids).map(String).filter(Boolean);
  if (!evidence.length && !items.length) return null;
  return (
    <details className="mt-2 rounded-lg border border-line bg-canvas px-2.5 py-2 text-[11px] text-ink-3">
      <summary className="cursor-pointer font-medium text-ink-2">
        Evidence and member items
      </summary>
      {evidence.length ? (
        <div className="mt-2">
          <div className="mb-1 text-[10.5px] font-semibold uppercase tracking-wide text-ink-3">
            Evidence
          </div>
          <div className="flex flex-wrap gap-1">
            {evidence.map((ref) => (
              <code key={ref} className="mono rounded-md bg-white px-1.5 py-0.5 text-[10px] text-ink-3">
                {ref}
              </code>
            ))}
          </div>
        </div>
      ) : null}
      {items.length ? (
        <div className="mt-2">
          <div className="mb-1 text-[10.5px] font-semibold uppercase tracking-wide text-ink-3">
            Items
          </div>
          <div className="flex flex-wrap gap-1">
            {items.map((id) => (
              <code key={id} className="mono rounded-md bg-white px-1.5 py-0.5 text-[10px] text-ink-3">
                {id}
              </code>
            ))}
          </div>
        </div>
      ) : null}
    </details>
  );
}

function ReviewGroupCard({ loaded }: { loaded: LoadedGroup }) {
  const { group, sample, card } = loaded;
  const summary = str(group.operator_summary) || card?.headline || "Review this decision.";
  const body = card?.body && card.body !== summary ? card.body : "";
  const context = str(group.context_label) || card?.area || areaOf(group);
  const cmd = actionCommand(group, sample);
  const action = group.safe_batch_action;
  const hasBatchAction = action && typeof action === "object";
  const reason = str(group.terminal_reason);
  const readiness = str(group.readiness) || card?.readiness || str(group.status);
  const count = num(group.count) || arr(group.item_ids).length || 1;

  return (
    <div className="rounded-xl border border-line bg-white px-4 py-3">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="min-w-0 break-words text-[13px] font-semibold leading-snug text-ink">
            {summary}
          </div>
          {context ? <div className="mt-0.5 text-[11px] text-ink-3">{context}</div> : null}
        </div>
        <div className="flex shrink-0 flex-wrap justify-end gap-1">
          <Chip tone="orange">{count} item{count === 1 ? "" : "s"}</Chip>
          {readiness ? <Chip tone="gray">{chipText(readiness)}</Chip> : null}
          {card?.confidence ? <Chip tone="orange">conf {card.confidence}</Chip> : null}
        </div>
      </div>

      {body ? <p className="mt-2 text-[12px] leading-relaxed text-ink-2">{body}</p> : null}

      {card?.warnings.length ? (
        <div className="mt-2 flex flex-wrap gap-1">
          {card.warnings.map((warning) => (
            <span key={warning} className="rounded-md bg-canvas px-1.5 py-0.5 text-[10px] text-ink-3">
              {warning}
            </span>
          ))}
        </div>
      ) : null}

      <div className="mt-2 rounded-lg bg-canvas px-2.5 py-1.5 text-[11px] leading-relaxed">
        <div className="text-ink-2">
          <span className="font-semibold text-emerald-700">if you act</span>
          <span className="text-ink-3">: </span>
          {str(group.operator_unlock) || card?.nextAction || "your recorded decision unblocks this group; nothing runs from this card"}
        </div>
        <div className="text-ink-3">
          <span className="font-semibold">if you wait</span>: this stays in Reviews.
          {reason ? ` ${reason}.` : ""}
        </div>
      </div>

      <EvidenceDrawer group={group} />

      <div className="mt-2 flex flex-wrap items-center gap-2">
        <CopyCmd cmd={cmd} compact />
        <span className="text-[10.5px] text-ink-3">
          {hasBatchAction ? "safe batch command exposed" : "copy command for review"}
          {card?.createdAt ? ` - ${ago(card.createdAt)}` : ""}
        </span>
        <code className="mono min-w-0 truncate text-[9.5px] text-ink-3">{str(group.key)}</code>
      </div>
    </div>
  );
}

export default async function Reviews() {
  const reviews = await getReviews();
  const counts = (reviews?.counts ?? {}) as Record<string, any>;
  const byArea = (counts.by_area ?? {}) as Record<string, number>;
  const externalRows = externalReviewRows(reviews);

  const loadedGroups = await loadGroups(groupRows(reviews), reviews);
  const byAreaGroups = new Map<string, LoadedGroup[]>();
  for (const loaded of loadedGroups) {
    const area = areaOf(loaded.group);
    byAreaGroups.set(area, [...(byAreaGroups.get(area) ?? []), loaded]);
  }
  const sections = [...byAreaGroups.entries()].sort((a, b) => {
    const delta = b[1].length - a[1].length;
    return delta || a[0].localeCompare(b[0]);
  });
  const quiet = Object.entries(byArea)
    .filter(([area]) => !byAreaGroups.has(area) && area !== "external-repo-intake")
    .sort((a, b) => b[1] - a[1]);
  const groupCount = num(counts.review_group_count) || loadedGroups.length;

  return (
    <div className="flex flex-col gap-8">
      <div className="flex items-end justify-between gap-4">
        <div>
          <h1 className="text-[22px] font-semibold tracking-tight">Reviews</h1>
          <p className="mt-0.5 text-[12.5px] text-ink-3">
            {num(counts.review_required)} items need your decision in {groupCount} decision group{groupCount === 1 ? "" : "s"}.
          </p>
        </div>
        <FreshnessChip generatedAt={str(reviews?.generated_at)} source="compound reviews --json" />
      </div>

      {externalRows.length ? (
        <Card
          title="Shared repos awaiting your call"
          accent="blue"
          glyph="E"
          right={<Chip tone="orange">{externalRows.length} open</Chip>}
        >
          <p className="mb-3 text-[11.5px] text-ink-3">{areaPlain("external-repo-intake")}</p>
          <div className="grid gap-3 lg:grid-cols-2">
            {externalRows.map((row) => (
              <DecisionCard key={str(row.id)} area="external-repo-intake" item={row} />
            ))}
          </div>
        </Card>
      ) : null}

      {sections.length ? (
        sections.map(([area, groups]) => (
          <section key={area}>
            <div className="mb-1 flex items-baseline justify-between gap-2">
              <h2 className="text-[14px] font-semibold text-ink">{area.replace(/-/g, " ")}</h2>
              <Chip tone="orange">{groups.length} group{groups.length === 1 ? "" : "s"}</Chip>
            </div>
            <p className="mb-3 text-[11.5px] text-ink-3">{areaPlain(area)}</p>
            <div className="grid gap-3 lg:grid-cols-2">
              {groups.slice(0, GROUP_CAP).map((loaded) => (
                <ReviewGroupCard key={str(loaded.group.key)} loaded={loaded} />
              ))}
            </div>
            {groups.length > GROUP_CAP ? (
              <p className="mt-2 text-[11px] text-ink-3">
                +{groups.length - GROUP_CAP} more groups in this area. <code className="mono">compound reviews --area {area}</code>
              </p>
            ) : null}
          </section>
        ))
      ) : !externalRows.length ? (
        <EmptyState>Nothing needs a decision right now.</EmptyState>
      ) : null}

      {quiet.length ? (
        <section>
          <h2 className="mb-2 text-[13px] font-semibold text-ink-2">Quiet areas</h2>
          <div className="flex flex-wrap gap-2">
            {quiet.map(([area, count]) => (
              <span
                key={area}
                title={areaPlain(area)}
                className="rounded-lg border border-line bg-white px-2.5 py-1 text-[11px] text-ink-3"
              >
                {area.replace(/-/g, " ")} - {count}
              </span>
            ))}
          </div>
        </section>
      ) : null}
    </div>
  );
}
