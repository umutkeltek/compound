import Link from "next/link";
import { appHref } from "@/lib/links";
import { PendingRefresh } from "@/components/PendingRefresh";
import { campaignAssetURL, getProductCampaigns, getWorkCampaigns, type VisualCampaign } from "@/lib/visual-campaigns";

export function VisualCampaignCard({ campaign, resultsHref = "/loops" }: { campaign: VisualCampaign; resultsHref?: string }) {
  const assets = campaign.assets ?? [];
  const available = assets.filter((asset) => asset.available);
  const expected = (campaign.locales?.length ?? 0) * (campaign.targets?.length ?? 0) * (campaign.scenes?.length ?? 0);
  const reviewed = available.filter((asset) => {
    const review = asset.review;
    return review && review.asset_sha256 === asset.sha256 && review.reviewer?.trim() && review.evidence_refs?.length && review.evidence_refs.every((ref) => ref.trim()) && review.coherence && review.copy_and_layout && review.capture_fidelity && review.platform_rules && review.native_detail;
  });
  return <article className="rounded-xl border border-hair bg-panel p-4 sm:p-5">
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div className="min-w-0">
        <h3 className="font-semibold text-ink">{campaign.direction?.concept || "Visuals awaiting a brief"}</h3>
        <p className="mt-1 text-sm text-muted">{campaign.context?.value || campaign.issue || "The attached campaign could not be read."}</p>
      </div>
      <span className="rounded-full bg-work-soft px-2.5 py-1 text-xs font-semibold text-work">{campaign.status === "unavailable" ? "Needs repair" : "Prepared for review"}</span>
    </div>
    <dl className="my-4 flex flex-wrap gap-x-7 gap-y-3 text-sm">
      <div><dt className="text-xs text-muted">Exports</dt><dd className="mt-1 font-semibold">{available.length} of {expected || "—"}</dd></div>
      <div><dt className="text-xs text-muted">Reviews recorded</dt><dd className="mt-1 font-semibold">{reviewed.length} of {available.length}</dd></div>
      <div><dt className="text-xs text-muted">Release and test</dt><dd className="mt-1 text-muted">Not established by this record</dd></div>
    </dl>
    {assets.length ? <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 xl:grid-cols-4">
      {assets.map((asset) => {
        const scene = campaign.scenes?.find((scene) => scene.id === asset.scene);
        const href = campaignAssetURL(campaign.work_id, campaign.index, asset.index);
        return <figure key={asset.index} className="min-w-0">
          {asset.available ? <a href={href} target="_blank" rel="noreferrer" className="block overflow-hidden rounded-lg border border-hair bg-soft" aria-label={`Open ${scene?.purpose || asset.scene}, ${asset.locale}, full resolution`}>
            {/* Native owner verifies image bytes and hash before serving. */}
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img src={href} alt={scene?.copy?.[asset.locale]?.headline || scene?.purpose || asset.scene} width={asset.width} height={asset.height} className="h-auto w-full" loading="lazy" />
          </a> : <div className="rounded-lg border border-dashed border-seam-line bg-seam-soft p-3 text-sm text-seam">{asset.issue || "Export unavailable"}</div>}
          <figcaption className="mt-2 text-xs text-muted"><span className="font-semibold text-ink">{asset.locale}</span>{asset.width && asset.height ? ` · ${asset.width} × ${asset.height}` : ""}<span className="mt-0.5 block">{scene?.purpose || asset.scene}</span></figcaption>
        </figure>;
      })}
    </div> : <p className="rounded-lg border border-dashed border-hair p-4 text-sm text-muted">The brief is attached. Exported images have not been attached yet.</p>}
    <div className="mt-4 border-t border-hair pt-4">
      <p className="text-sm font-semibold text-ink">Next: {campaign.status === "unavailable" ? "repair the attached campaign" : available.length < expected ? "complete the missing exports" : "review the complete set"}</p>
      <p className="mt-1 text-sm text-muted">{available.length ? "Confirm the current app screens, review each language, then use the release workflow. Improvement is measured after exposure." : "The work record holds the brief, owner and progress."}</p>
      <div className="mt-3 flex flex-wrap gap-4 text-sm font-semibold"><Link className="text-work hover:underline" href={`/work/${campaign.work_id}`}>Open visual work →</Link><Link className="text-work hover:underline" href={resultsHref}>Review measured results</Link></div>
    </div>
    <details className="mt-4 text-xs text-muted"><summary className="cursor-pointer">Checks and source details</summary>
      <p className="mt-2">{campaign.check?.ready ? "File checks passed. This does not approve publication or prove conversion lift." : campaign.check_error || campaign.issue || "File checks are incomplete."}</p>
      {campaign.check?.issues?.length ? <ul className="mt-2 list-disc space-y-1 pl-4">{campaign.check.issues.map((issue) => <li key={issue}>{issue}</li>)}</ul> : null}
      {campaign.context?.constraints?.length ? <ul className="mt-2 list-disc space-y-1 pl-4">{campaign.context.constraints.map((text) => <li key={text}>{text}</li>)}</ul> : null}
      <p className="mt-2">Languages requested: {campaign.locales?.map((locale) => locale.tag).join(", ") || "None recorded"}. Only exported images appear above.</p>
    </details>
  </article>;
}

/**
 * An app's marketing visuals. Shown as a section only when its work holds a
 * visual campaign; otherwise one quiet line (also while the work read is
 * still running), never an empty block that looks like an answer.
 */
export async function ProductVisuals({ productIds }: { productIds: string[] }) {
  const report = await getProductCampaigns(productIds);
  return <ProductVisualsView report={report} resultsHref={`${appHref(productIds[0] ?? "")}#app-results`} />;
}

export function ProductVisualsView({ report, resultsHref }: { report: Awaited<ReturnType<typeof getProductCampaigns>>; resultsHref: string }) {
  if (!report.campaigns.length) {
    return <div id="visuals" className="scroll-mt-24 text-sm text-muted" data-visuals-quiet>
      <PendingRefresh pending={report.pending} stamp={Date.now()} />
      <span className="font-medium text-ink">Marketing visuals:</span>{" "}
      {report.pending ? "still reading this app's work." : "none in this app's open or recent work."}
      {report.issue ? <span className="ml-1 text-xs text-faint">{report.issue}</span> : null}
    </div>;
  }
  return <section id="visuals" className="scroll-mt-24 space-y-3" aria-labelledby="visuals-title">
    <PendingRefresh pending={report.pending} stamp={Date.now()} />
    <h2 id="visuals-title" className="text-lg font-semibold text-ink">Marketing visuals</h2>
    <p className="text-sm text-muted">Images and release checks from open and recent work on this app.</p>
    {report.issue ? <p className="text-sm text-seam">{report.issue}</p> : null}
    {report.campaigns.map((campaign) => <VisualCampaignCard key={`${campaign.work_id}:${campaign.index}`} campaign={campaign} resultsHref={resultsHref} />)}
    {report.limited ? <p className="text-xs text-muted">Showing the first 12 visual work records. Open Work for the rest.</p> : null}
  </section>;
}

export async function WorkVisuals({ workId }: { workId: string }) {
  const campaigns = await getWorkCampaigns(workId);
  return <section className="space-y-3" aria-label="Visual work outputs"><h2 className="text-lg font-semibold">Visual outputs</h2>{campaigns ? campaigns.map((campaign) => <VisualCampaignCard key={campaign.index} campaign={campaign} />) : <p className="text-sm text-seam">The attached visuals could not be read. Refresh this work record to retry.</p>}</section>;
}
