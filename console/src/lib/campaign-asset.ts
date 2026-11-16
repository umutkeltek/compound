import { createHash } from "node:crypto";
import type { VisualCampaign } from "./visual-campaigns";
import { isWorkId } from "./work-id";

export type CampaignAssetReader = {
  campaigns: (workId: string) => Promise<VisualCampaign[] | null>;
  bytes: (workId: string, campaign: string, asset: string) => Promise<Uint8Array>;
};

/** Recheck returned bytes against the metadata the image request resolved.
 * A manifest edit or attachment reorder between the two native reads must
 * return a conflict, never an image belonging to a different index version. */
export async function campaignAssetResponse(request: Request, reader: CampaignAssetReader): Promise<Response> {
  const url = new URL(request.url);
  if (request.headers.get("sec-fetch-site") === "cross-site") return new Response("Cross-site asset reads are unavailable.", { status: 403 });
  const workId = url.searchParams.get("work") || "";
  const campaign = url.searchParams.get("campaign") || "";
  const asset = url.searchParams.get("asset") || "";
  if (!isWorkId(workId) || !/^(0|[1-9][0-9]{0,5})$/.test(campaign) || !/^(0|[1-9][0-9]{0,5})$/.test(asset)) return new Response("Invalid image reference.", { status: 400 });
  try {
    const rows = await reader.campaigns(workId);
    const entry = rows?.find((row) => row.index === Number(campaign))?.assets?.find((row) => row.index === Number(asset));
    if (!entry?.available || !["image/png", "image/jpeg"].includes(entry.mime_type ?? "")) return new Response("This image is unavailable. Refresh its work record for details.", { status: 404 });
    const bytes = await reader.bytes(workId, campaign, asset);
    const png = bytes.length >= 8 && [137, 80, 78, 71, 13, 10, 26, 10].every((value, index) => bytes[index] === value);
    const jpeg = bytes.length >= 3 && bytes[0] === 255 && bytes[1] === 216 && bytes[2] === 255;
    const mime = png ? "image/png" : jpeg ? "image/jpeg" : "";
    if (bytes.byteLength > 20 * 1024 * 1024 || !mime || mime !== entry.mime_type || createHash("sha256").update(bytes).digest("hex") !== entry.sha256) return new Response("This image changed. Refresh its work record.", { status: 409 });
    return new Response(new Uint8Array(bytes), { headers: { "content-type": mime, "cache-control": "private, no-store", "x-content-type-options": "nosniff" } });
  } catch {
    return new Response("Image verification failed. Refresh its work record for details.", { status: 409 });
  }
}
