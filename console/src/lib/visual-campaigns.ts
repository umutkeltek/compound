import { arr, readProjection, runJson, str, type Json, type LiveRead } from "@/lib/compound";

export type VisualAsset = {
  index: number; scene: string; locale: string; target: string; sha256: string;
  available: boolean; issue?: string; width?: number; height?: number; mime_type?: string;
  review?: { asset_sha256: string; reviewer: string; evidence_refs: string[]; coherence: boolean; copy_and_layout: boolean; capture_fidelity: boolean; platform_rules: boolean; native_detail: boolean };
};
export type VisualCampaign = {
  index: number; work_id: string; id?: string; product_id?: string; status: string; issue?: string;
  context?: { audience: string; problem: string; value: string; constraints: string[] };
  direction?: { concept: string };
  locales?: { tag: string }[]; targets?: { id: string; width: number; height: number }[];
  scenes?: { id: string; purpose: string; copy: Record<string, { headline: string }> }[];
  assets?: VisualAsset[]; check?: { ready: boolean; issues?: string[]; expected_assets?: number; verified_assets?: number }; check_error?: string;
};
export const campaignViewArgs = (workId: string) => ["product", "campaign", "view", "--work", workId, "--json"];
export function campaignWorkIds(report: Record<string, unknown>): string[] {
  return [...new Set([...arr(report.open), ...arr(report.recent)]
    .filter((row) => arr(row.artifact_ids).some((ref) => typeof ref === "string" && ref.startsWith("visual-campaign:")))
    .map((row) => str(row.work_id)).filter(Boolean))];
}
export async function getWorkCampaigns(workId: string, read: (args: string[]) => Promise<Json | null> = runJson): Promise<VisualCampaign[] | null> {
  const report = await read(campaignViewArgs(workId));
  if (report?.schema_version !== "compound.visual_campaign_view.v0.1" || report.work_id !== workId || !Array.isArray(report.campaigns)) return null;
  return report.campaigns as VisualCampaign[];
}
export function campaignAssetURL(workId: string, campaign: number, asset: number): string {
  return `/api/campaign/asset?work=${encodeURIComponent(workId)}&campaign=${campaign}&asset=${asset}`;
}
/** Reuses work's artifact inventory, including portfolio work naming several apps. */
export async function getProductCampaigns(productIds: string[], deps: {
  inventory: () => Promise<Pick<LiveRead, "value" | "status" | "updating" | "error">>;
  campaigns: (workId: string) => Promise<VisualCampaign[] | null>;
} = { inventory: () => readProjection(["workbench", "--json"]), campaigns: getWorkCampaigns }) {
  const read = await deps.inventory();
  if (!read.value) return { campaigns: [] as VisualCampaign[], pending: read.status === "loading", issue: read.error || "Work is still being read.", limited: false };
  const ids = campaignWorkIds(read.value);
  const reports = await Promise.all(ids.slice(0, 12).map((id) => deps.campaigns(id)));
  const wanted = new Set(productIds.map((id) => id.replace(/^product:/, "")));
  return {
    campaigns: reports.flatMap((rows) => rows ?? []).filter((c) => wanted.has(str(c.product_id).replace(/^product:/, ""))),
    pending: read.updating,
    issue: reports.some((rows) => rows === null || rows.some((row) => row.status === "unavailable")) ? "Some visual work could not be read. Open its work record to retry." : read.status === "stale" ? "Showing the last successful work inventory." : "",
    limited: ids.length > 12,
  };
}
