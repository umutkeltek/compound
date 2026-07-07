import { getInstalledBinaryFreshness } from "@/lib/compound";

export async function SystemBanner() {
  const freshness = await getInstalledBinaryFreshness();
  if (freshness.status !== "stale") return null;

  return (
    <div
      title={`${freshness.message} Hooks and live consumers may still run old code.`}
      className="mr-auto max-w-[620px] truncate rounded-full border border-amber-200 bg-amber-50 px-3 py-1 text-[11.5px] font-medium text-amber-800"
    >
      Installed compound is stale; hooks may not see latest code. Run <code className="mono">make install</code>.
    </div>
  );
}
