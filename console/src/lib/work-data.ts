import { arr, getWorkbench, Json, runJson, str } from "@/lib/compound";

export type PacketSummary = {
  path: string;
  title: string;
  objective: string;
  success: string;
  boundaries: string[];
  canonical_surface?: string;
  evidence_ref?: string;
  source_digest?: string;
};

export type WorkCardData = Json & {
  displayId: string;
  packet: PacketSummary | null;
};

export function workId(item: Json): string {
  return str(item.work_id) || str(item.id);
}

export function shortText(value: string, max = 300): string {
  const compact = value.replace(/\s+/g, " ").trim();
  if (compact.length <= max) return compact;
  return `${compact.slice(0, max - 1).trimEnd()}...`;
}

export async function packetForSurface(surface: string): Promise<PacketSummary | null> {
  if (!surface.startsWith("packet:")) return null;
  const data = await runJson(["packets", "list", "--json"]);
  const item = arr(data?.items).find((candidate) => str(candidate.canonical_surface) === surface);
  if (!item) return null;
  return {
    path: str(item.path) || str(item.source_ref),
    title: str(item.title),
    objective: "",
    success: "",
    boundaries: [],
    canonical_surface: str(item.canonical_surface),
    evidence_ref: str(item.evidence_ref),
    source_digest: str(item.source_digest),
  };
}

export async function enrichWorkItem(item: Json): Promise<WorkCardData> {
  const id = workId(item);
  const packet = await packetForSurface(str(item.canonical_surface));
  return { ...item, displayId: id, packet };
}

export async function enrichWorkItems(items: Json[]): Promise<WorkCardData[]> {
  return Promise.all(items.map(enrichWorkItem));
}

export async function findWorkbenchItem(id: string): Promise<Json | null> {
  const workbench = await getWorkbench();
  const items = [...arr(workbench?.open), ...arr(workbench?.recent)];
  return items.find((item) => workId(item) === id) ?? null;
}
