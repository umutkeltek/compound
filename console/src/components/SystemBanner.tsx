import Link from "next/link";
import { getInstalledBinaryFreshness } from "@/lib/compound";

export async function SystemBanner({ compact = false }: { compact?: boolean }) {
  const freshness = await getInstalledBinaryFreshness();
  if (freshness.status !== "stale") return null;
  return (
    <Link href="/settings" title={freshness.message}
      className={compact ? "mx-4 mt-2 block text-xs text-muted hover:underline" : "mr-auto text-xs text-muted hover:underline"}>
      A newer Compound build is available <span aria-hidden>→</span>
    </Link>
  );
}
