import { Nav } from "@/components/Nav";
import { NavAppLinks, type NavApp } from "@/components/NavAppLinks";
import { InstanceBadge } from "@/components/InstanceBadge";
import { PendingRefresh } from "@/components/PendingRefresh";
import { ReadChip, readsPending } from "@/components/kit";
import { readAttention } from "@/lib/attention";
import { waitingOnYou } from "@/lib/counts";
import Link from "next/link";
import { autonomyPosture } from "@/lib/briefing";
import { settingsHref } from "@/lib/links";
import { plainAutonomy } from "@/lib/plain-words";
import { readProjection } from "@/lib/compound";
import { appHue, appSlug, mapGrowthModel } from "@/lib/growth";

/**
 * Server wrapper for the sidebar. It never waits for compound: every read is
 * the non-blocking `readProjection` (a cached copy at once, or "loading"),
 * so the shell renders immediately on every page.
 *
 * The sidebar's one count, on Today, comes from the ONE "waiting on you"
 * definition (`waitingOnYou` over `compound attention --json`), the same
 * function the Today header uses: decisions waiting for you. Before attention
 * has been read there is no count (unknown is not zero).
 *
 * The Apps list reads `compound product growth --json` through the same
 * cache key `getGrowth` uses, and maps it with growth's own mapper.
 */
const GROWTH_ARGS = ["product", "growth", "--json"];

async function NavApps() {
  const read = await readProjection(GROWTH_ARGS, { timeoutMs: 60_000 });
  const model = read.value ? mapGrowthModel(read.value) : null;
  if (!model) {
    return (
      <NavAppLinks
        apps={[]}
        total={0}
        note={
          read.status === "loading"
            ? "Reading app data…"
            : read.status === "unreachable"
              ? "Could not read app data."
              : "No apps with data."
        }
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

/**
 * The sidebar status card, in plain words: what agents may do without asking
 * (lib/plain-words.ts plainAutonomy, the same sentence Today uses), when the
 * waiting list was last read, and a link to the Settings section that owns
 * it. The instance badge shows here only when this server is unpinned (a
 * warning); otherwise it lives on Settings.
 */
async function NavStatus() {
  const [{ read: attentionRead }, policyRead] = await Promise.all([
    readAttention(),
    readProjection(["policy", "operator-authority", "--json"]),
  ]);
  const autonomy = policyRead.value ? autonomyPosture(policyRead.value) : null;
  const plain = autonomy ? plainAutonomy(autonomy.level) : "";
  return (
    <div className="rounded-xl border border-hair bg-panel p-3 shadow-sm" data-status-card>
      <div className="flex items-center gap-2 text-2xs text-faint">
        <span>What agents may do</span>
        {autonomy ? <span className={`h-1.5 w-1.5 rounded-full ${autonomy.tone === "good" ? "bg-good" : "bg-seam"}`} aria-hidden /> : null}
      </div>
      <p className="mt-1 text-xs leading-snug text-ink">
        {autonomy
          ? plain || `Their permission level is ${autonomy.level}.`
          : policyRead.status === "unreachable" ? "Could not read what agents may do." : "Reading what agents may do…"}
      </p>
      <div className="mt-2.5 flex flex-wrap items-center gap-1.5 text-2xs text-faint">
        <span>Waiting list</span>
        <ReadChip read={attentionRead} />
        <InstanceBadge onlyWhenUnpinned />
      </div>
      <Link href={settingsHref("safety-permissions")} className="mt-2 inline-block text-xs font-semibold text-work hover:underline">
        Safety and permissions →
      </Link>
    </div>
  );
}

export async function NavContainer({ variant = "desktop" }: { variant?: "desktop" | "mobile" }) {
  const [{ read, report }, growthRead, policyRead] = await Promise.all([
    readAttention(),
    readProjection(GROWTH_ARGS, { timeoutMs: 60_000, enabled: false }),
    readProjection(["policy", "operator-authority", "--json"], { enabled: false }),
  ]);
  const waiting = waitingOnYou(report);
  return (
    <>
      <Nav
        decideCount={waiting?.groups ?? null}
        variant={variant}
        apps={<NavApps />}
        status={<NavStatus />}
      />
      {variant === "desktop" ? (
        <PendingRefresh pending={readsPending(read, growthRead, policyRead)} stamp={Date.now()} />
      ) : null}
    </>
  );
}
