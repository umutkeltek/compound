import { Suspense } from "react";
import { Nav } from "@/components/Nav";
import { NavAppLinks, type NavApp } from "@/components/NavAppLinks";
import { InstanceBadge } from "@/components/InstanceBadge";
import { FreshnessChip } from "@/components/kit";
import { attentionNeedsYouGroups, getAttention } from "@/lib/attention";
import { autonomyPosture } from "@/lib/briefing";
import { runJson, str } from "@/lib/compound";
import { appHue, appSlug, getGrowth } from "@/lib/growth";

/**
 * Server wrapper for the sidebar. Badge counts come from ONE attention
 * projection (`compound attention --json`) through the shared runJson TTL memo:
 * every "what needs you" consumer reads the same projection, so the sidebar
 * can never disagree with the briefing.
 *
 * - Decide badge = `counts.needs_you_groups` (system-wide, all sources).
 * - Work badge = needs-you groups sourced from the workbench.
 *
 * The Apps list reads `compound product growth --json`, which can be slow on a
 * cold cache; it streams in behind Suspense so the shell never waits for it.
 */
async function NavApps() {
  const growth = await getGrowth();
  const model = growth.model;
  if (!model) {
    return (
      <NavAppLinks
        apps={[]}
        total={0}
        note={growth.status === "unreachable" ? "App data not loaded yet." : "No apps with data."}
      />
    );
  }
  const rank = new Map(model.portfolio.allocation.map((row) => [row.productId, row.rank]));
  const hasData = (sources: number) => (sources > 0 ? 0 : 1);
  const apps: NavApp[] = [...model.apps]
    .sort(
      (a, b) =>
        hasData(a.freshness.sources.length) - hasData(b.freshness.sources.length) ||
        (rank.get(a.productId) ?? 999) - (rank.get(b.productId) ?? 999) ||
        a.name.localeCompare(b.name),
    )
    .map((app) => ({
      slug: appSlug(app.productId),
      name: app.name,
      hue: appHue(app.productId),
      stale: app.freshness.stale,
      noData: app.freshness.sources.length === 0,
      kind: app.kind,
    }));
  return <NavAppLinks apps={apps} total={apps.length} />;
}

function NavAppsFallback() {
  return (
    <div className="flex flex-col gap-1.5 px-2.5" aria-hidden>
      <div className="pb-1 text-xs font-medium text-faint">Apps</div>
      {[0, 1, 2, 3].map((i) => (
        <div key={i} className="skeleton h-5" />
      ))}
    </div>
  );
}

async function NavStatus() {
  const [attention, policyAuthority] = await Promise.all([
    getAttention(),
    runJson(["policy", "operator-authority", "--json"]),
  ]);
  const autonomy = autonomyPosture(policyAuthority);
  return (
    <div className="rounded-xl border border-hair bg-panel p-3 shadow-sm">
      <div className="text-2xs text-faint">Autonomy</div>
      <div className="flex items-baseline gap-2">
        <span className="text-xl font-semibold tracking-tight text-ink">{autonomy.level}</span>
        <span className={`h-1.5 w-1.5 rounded-full ${autonomy.tone === "good" ? "bg-good" : "bg-seam"}`} aria-hidden />
      </div>
      <p className="mt-1 text-xs leading-snug text-muted" title="Kill switch: compound policy autonomy-halt · source: compound policy operator-authority --json">
        {autonomy.detail}
      </p>
      <div className="mt-2.5 flex flex-wrap items-center gap-1.5">
        <FreshnessChip generatedAt={str(attention?.generated_at) || undefined} source="compound attention --json" />
        <InstanceBadge />
      </div>
    </div>
  );
}

export async function NavContainer({ variant = "desktop" }: { variant?: "desktop" | "mobile" }) {
  const attention = await getAttention();
  const decideCount = attention?.counts?.needs_you_groups ?? 0;
  const workCount = attentionNeedsYouGroups(attention)
    .filter((group) => group.source === "workbench")
    .reduce((sum, group) => sum + group.count, 0);
  return (
    <Nav
      decideCount={decideCount}
      workCount={workCount}
      variant={variant}
      apps={
        <Suspense fallback={<NavAppsFallback />}>
          <NavApps />
        </Suspense>
      }
      status={<NavStatus />}
    />
  );
}
