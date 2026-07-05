import { readdir, readFile } from "node:fs/promises";
import path from "node:path";

import { arr, getWorkbench, Json, privateFallbacksEnabled, str } from "@/lib/compound";

export type PacketSummary = {
  path: string;
  title: string;
  objective: string;
  success: string;
  boundaries: string[];
};

export type WorkCardData = Json & {
  displayId: string;
  packet: PacketSummary | null;
};

const PACKET_DIR = path.resolve(process.cwd(), "..", "docs_private", "plan", "packets");

export function workId(item: Json): string {
  return str(item.work_id) || str(item.id);
}

export function workActionArgv(id: string, action: "admit" | "cancel"): string[] {
  if (action === "admit") {
    return ["work", "advance", id, "--to", "admitted", "--by", "operator-console"];
  }
  return [
    "work",
    "close",
    id,
    "--outcome",
    "cancelled",
    "--move-type",
    "audit_only",
    "--by",
    "operator-console",
    "--summary",
    "operator cancelled proposed work",
  ];
}

export function workActionCommand(id: string, action: "admit" | "cancel"): string {
  return ["compound", ...workActionArgv(id, action)].join(" ");
}

function slug(value: string): string {
  return value
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

function firstSectionParagraph(lines: string[], heading: string): string {
  let inSection = false;
  const out: string[] = [];

  for (const line of lines) {
    const trimmed = line.trim();
    if (/^##\s+/.test(trimmed)) {
      const name = trimmed.replace(/^##\s+/, "").trim();
      if (inSection) break;
      if (name.toLowerCase() === heading.toLowerCase()) inSection = true;
      continue;
    }
    if (!inSection) continue;
    if (!trimmed) {
      if (out.length) break;
      continue;
    }
    if (trimmed.startsWith("- ")) break;
    out.push(trimmed);
  }

  return out.join(" ").trim();
}

function metaParagraph(lines: string[], key: string): string {
  const prefix = `${key}:`;
  for (const line of lines) {
    const trimmed = line.trim();
    if (trimmed.toLowerCase().startsWith(prefix.toLowerCase())) {
      return trimmed.slice(prefix.length).trim();
    }
  }
  return "";
}

function sectionBullets(lines: string[], heading: string): string[] {
  let inSection = false;
  const out: string[] = [];

  for (const line of lines) {
    const trimmed = line.trim();
    if (/^##\s+/.test(trimmed)) {
      const name = trimmed.replace(/^##\s+/, "").trim();
      if (inSection) break;
      if (name.toLowerCase() === heading.toLowerCase()) inSection = true;
      continue;
    }
    if (inSection && trimmed.startsWith("- ")) out.push(trimmed.slice(2).trim());
  }

  return out;
}

function parsePacketMarkdown(source: string, filePath: string): PacketSummary {
  const lines = source.split(/\r?\n/);
  const title = lines.find((line) => line.startsWith("# "))?.slice(2).trim() || path.basename(filePath);
  return {
    path: filePath,
    title,
    objective: firstSectionParagraph(lines, "Objective"),
    success: metaParagraph(lines, "Success"),
    boundaries: sectionBullets(lines, "Boundaries"),
  };
}

export function shortText(value: string, max = 300): string {
  const compact = value.replace(/\s+/g, " ").trim();
  if (compact.length <= max) return compact;
  return `${compact.slice(0, max - 1).trimEnd()}...`;
}

export async function packetForSurface(surface: string): Promise<PacketSummary | null> {
  if (!surface.startsWith("packet:")) return null;
  if (!privateFallbacksEnabled()) return null;
  const wanted = surface.slice("packet:".length);

  try {
    const entries = await readdir(PACKET_DIR, { withFileTypes: true });
    for (const entry of entries) {
      if (!entry.isFile() || !entry.name.endsWith(".md")) continue;
      const filePath = path.join(PACKET_DIR, entry.name);
      const source = await readFile(filePath, "utf8");
      const packet = parsePacketMarkdown(source, filePath);
      const titleSlug = slug(packet.title);
      const fileSlug = slug(entry.name.replace(/\.md$/, ""));
      if (titleSlug === wanted || fileSlug === wanted || fileSlug === `packet-${wanted}`) {
        return packet;
      }
    }
  } catch {
    return null;
  }

  return null;
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
