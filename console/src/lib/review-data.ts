import { readFile } from "node:fs/promises";
import path from "node:path";

import { arr, COMPOUND_HOME, Json, reviewCommand, str } from "@/lib/compound";
import { parseReviewDraftYaml, ReviewDraftFields } from "@/lib/review-yaml";

export type ExternalReviewRow = Json & {
  yamlPath: string;
  draft: ReviewDraftFields | null;
};

const EXTERNAL_AREA = "external-repo-intake";

export function externalReviewYamlPath(id: string): string {
  const reviewsRoot = path.resolve(COMPOUND_HOME, "reviews");
  const resolved = path.resolve(reviewsRoot, EXTERNAL_AREA, `${id}.review.yaml`);
  if (!resolved.startsWith(reviewsRoot + path.sep)) {
    throw new Error("review YAML path escaped reviews root");
  }
  return resolved;
}

function rowKey(item: Json): string {
  return `${str(item.area) || str(item.review_area)}:${str(item.id) || str(item.review_id)}`;
}

export function externalReviewRows(reviews: Json | null, workbench: Json | null = null): Json[] {
  const sources = [
    ...arr(reviews?.items),
    ...arr(reviews?.actionable),
    ...arr(reviews?.review_queue?.actionable),
    ...arr(workbench?.review_actions),
    ...arr(workbench?.reviews),
    ...arr(workbench?.review_queue?.actionable),
  ];

  const merged = new Map<string, Json>();
  for (const item of sources) {
    const key = rowKey(item);
    if (!key || key === ":") continue;
    const prev = merged.get(key) ?? {};
    merged.set(key, {
      ...prev,
      ...item,
      actions: arr(prev.actions).length ? arr(prev.actions) : arr(item.actions),
      boundaries: arr(prev.boundaries).length ? arr(prev.boundaries) : arr(item.boundaries),
      evidence_refs: arr(prev.evidence_refs).length ? arr(prev.evidence_refs) : arr(item.evidence_refs),
    });
  }

  return [...merged.values()].filter((item) => {
    const area = str(item.area) || str(item.review_area);
    const status = `${str(item.status)} ${str(item.readiness)} ${str(item.next_action)}`.toLowerCase();
    return area === EXTERNAL_AREA && status.includes("review") && status.includes("required");
  });
}

export async function loadExternalReviewRow(item: Json): Promise<ExternalReviewRow> {
  const id = str(item.id) || str(item.review_id);
  const yamlPath = externalReviewYamlPath(id);
  try {
    const source = await readFile(yamlPath, "utf8");
    return { ...item, yamlPath, draft: parseReviewDraftYaml(source) };
  } catch {
    return { ...item, yamlPath, draft: null };
  }
}

export async function loadExternalReviewRows(
  reviews: Json | null,
  workbench: Json | null = null,
): Promise<ExternalReviewRow[]> {
  return Promise.all(externalReviewRows(reviews, workbench).map(loadExternalReviewRow));
}

export function externalReviewCommand(row: ExternalReviewRow): string {
  if (row.yamlPath) return `compound capability external-intake-review ${row.yamlPath}`;
  return reviewCommand(row);
}
