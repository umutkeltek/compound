import { runCompoundBytes } from "@/lib/compound";
import { getWorkCampaigns } from "@/lib/visual-campaigns";
import { campaignAssetResponse } from "@/lib/campaign-asset";

export const dynamic = "force-dynamic";
export async function GET(request: Request) {
  return campaignAssetResponse(request, {
    campaigns: getWorkCampaigns,
    bytes: (workId, campaign, asset) =>
      runCompoundBytes(["product", "campaign", "view", "--work", workId, "--campaign", campaign, "--asset", asset], { timeoutMs: 15_000, maxBytes: 20 * 1024 * 1024 }),
  });
}
