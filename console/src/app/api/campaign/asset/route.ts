import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { COMPOUND_BIN, COMPOUND_HOME } from "@/lib/compound";
import { consoleChildEnv } from "@/lib/console-child-env";
import { getWorkCampaigns } from "@/lib/visual-campaigns";
import { campaignAssetResponse } from "@/lib/campaign-asset";

const exec = promisify(execFile);
export const dynamic = "force-dynamic";
export async function GET(request: Request) {
  return campaignAssetResponse(request, {
    campaigns: getWorkCampaigns,
    bytes: async (workId, campaign, asset) => {
      const result = await exec(COMPOUND_BIN, ["product", "campaign", "view", "--work", workId, "--campaign", campaign, "--asset", asset], { env: consoleChildEnv({ COMPOUND_HOME }), encoding: "buffer", timeout: 15_000, maxBuffer: 20 * 1024 * 1024 });
      return new Uint8Array(result.stdout);
    },
  });
}
