import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { campaignWorkIds, getWorkCampaigns, getProductCampaigns, type VisualCampaign, type VisualAsset } from "./visual-campaigns";
import { campaignAssetResponse } from "./campaign-asset";
import { VisualCampaignCard } from "@/components/products/VisualCampaigns";

export async function runVisualCampaignsSelfTest(): Promise<number> {
  let checks = 0;
  const check = (value: unknown, message: string) => { assert.ok(value, message); checks++; };
  const workId = "work_portfolio_visuals_20300220";
  const campaign: VisualCampaign = {
    index: 0, work_id: workId, id: "campaign_fixture", product_id: "product:synthetic-app", status: "draft",
    context: { audience: "Players", problem: "Scoring interrupts play", value: "Stay in the game", constraints: ["Show only supported features"] },
    direction: { concept: "Friends playing together" }, locales: [{ tag: "en-US" }, { tag: "ar" }], targets: [{ id: "store", width: 16, height: 24 }],
    scenes: [{ id: "friends", purpose: "Show shared play", copy: { "en-US": { headline: "Stay in the game" } } }], assets: [],
    check: { ready: false, expected_assets: 2, verified_assets: 0, issues: ["friends/ar/store: export missing"] },
  };
  const inventory = {
    open: [{ work_id: workId, product_id: "portfolio", artifact_ids: ["visual-campaign:/synthetic/campaign.json"] }, { work_id: "work_unrelated", artifact_ids: ["image:other"] }],
    recent: [{ work_id: workId, artifact_ids: ["visual-campaign:/synthetic/campaign.json"] }],
  };
  check(JSON.stringify(campaignWorkIds(inventory)) === JSON.stringify([workId]), "Campaign discovery uses attached artifacts, deduplicates work, and includes cross-product portfolio work");
  const live = { value: inventory, status: "live" as const, updating: false, error: null };
  const seen: string[] = [];
  const product = await getProductCampaigns(["synthetic-app"], { inventory: async () => live, campaigns: async (id) => { seen.push(id); return [campaign]; } });
  check(product.campaigns.length === 1 && seen[0] === workId && !product.issue, "Cross-product work resolves by the native campaign product, not the work's portfolio label");
  const unavailable = await getProductCampaigns(["product:synthetic-app"], { inventory: async () => live, campaigns: async () => [{ index: 0, work_id: workId, status: "unavailable", issue: "Manifest unreadable" }] });
  check(unavailable.issue.includes("could not be read") && unavailable.campaigns.length === 0, "Unavailable campaign without product_id is an explicit failure, not a crash or clean empty result");
  const empty = await getProductCampaigns(["synthetic-app"], { inventory: async () => ({ ...live, value: { open: [], recent: [] } }), campaigns: async () => { throw new Error("Must not read unattached work"); } });
  check(empty.campaigns.length === 0 && !empty.issue && !empty.pending, "A confirmed empty artifact inventory is distinct from a failed read");
  const failed = await getProductCampaigns(["synthetic-app"], { inventory: async () => ({ ...live, value: null, status: "unreachable", error: "Inventory unavailable" }), campaigns: async () => [] });
  check(failed.issue === "Inventory unavailable" && !failed.pending, "Failed inventory preserves its exact recovery issue");
  check(await getWorkCampaigns(workId, async () => ({})) === null, "Malformed response cannot become a successful empty list");
  check(await getWorkCampaigns(workId, async () => ({ schema_version: "compound.visual_campaign_view.v0.1", work_id: "work_other", campaigns: [] })) === null, "Wrong-work metadata cannot be accepted");
  check((await getWorkCampaigns(workId, async () => ({ schema_version: "compound.visual_campaign_view.v0.1", work_id: workId, campaigns: [] })))?.length === 0, "Native confirmed empty list remains empty");

  const html = renderToStaticMarkup(createElement(VisualCampaignCard, { campaign }));
  check(html.includes("0 of 2") && html.includes("export missing") && html.includes("complete the missing exports"), "Missing exports stay counted and the exact native check issue is accessible");
  check(html.includes("Not established by this record") && !html.includes("finish the release checks"), "Draft visuals do not claim release, exposure or measured impact");
  check(html.includes(`/work/${workId}`) && !html.includes("<img"), "Empty image set offers the owning work without fake images");

  const png = Uint8Array.from([137, 80, 78, 71, 13, 10, 26, 10, 1, 2, 3]);
  // Native owner validates complete image decoding. This transport test uses
  // minimal signature bytes to isolate metadata/asset request identity.
  const asset: VisualAsset = { index: 0, scene: "friends", locale: "en-US", target: "store", available: true, mime_type: "image/png", sha256: createHash("sha256").update(png).digest("hex") };
  const withAsset = { ...campaign, assets: [asset] };
  const request = (query = `work=${workId}&campaign=0&asset=0`) => new Request(`https://console.example/api/campaign/asset?${query}`);
  const good = await campaignAssetResponse(request(), { campaigns: async () => [withAsset], bytes: async () => png });
  check(good.status === 200 && good.headers.get("content-type") === "image/png" && good.headers.get("cache-control") === "private, no-store" && good.headers.get("x-content-type-options") === "nosniff", "Verified image bytes have exact MIME and private uncached response headers");
  check(Buffer.from(await good.arrayBuffer()).equals(Buffer.from(png)), "Successful endpoint returns the exact native verified bytes");
  const changed = await campaignAssetResponse(request(), { campaigns: async () => [withAsset], bytes: async () => Uint8Array.from([...png, 4]) });
  check(changed.status === 409 && !(await changed.text()).includes("PNG"), "Manifest/index change between metadata and bytes yields a conflict with no image payload");
  const reordered = await campaignAssetResponse(request(), { campaigns: async () => [{ ...withAsset, index: 1 }], bytes: async () => { throw new Error("Wrong campaign index must not read bytes"); } });
  check(reordered.status === 404, "Missing campaign index never selects a different attachment");
  const wrongMime = await campaignAssetResponse(request(), { campaigns: async () => [{ ...withAsset, assets: [{ ...asset, mime_type: "image/jpeg" }] }], bytes: async () => png });
  check(wrongMime.status === 409, "MIME changes cannot pass solely because the byte hash matches");
  for (const query of [`work=work_../secret&campaign=0&asset=0`, `work=${workId}&campaign=-1&asset=0`, `work=${workId}&campaign=0&asset=../../private`]) {
    const invalid = await campaignAssetResponse(request(query), { campaigns: async () => { throw new Error("Invalid query reached native reader"); }, bytes: async () => png });
    check(invalid.status === 400, "Asset endpoint rejects non-index paths and malformed work before reading");
  }
  const crossSite = await campaignAssetResponse(new Request("https://console.example/api/campaign/asset", { headers: { "sec-fetch-site": "cross-site" } }), { campaigns: async () => [], bytes: async () => png });
  check(crossSite.status === 403, "Cross-site request cannot enter native asset reads");
  const oversized = await campaignAssetResponse(request(), { campaigns: async () => [withAsset], bytes: async () => new Uint8Array(20 * 1024 * 1024 + 1) });
  check(oversized.status === 409, "Transport independently refuses bytes beyond the native image budget");
  return checks;
}
