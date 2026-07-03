import type { Accent } from "@/components/ui";

/*
 * Shared types for the Map surface. No functions cross the server→client
 * boundary here — these are pure data shapes the topology loader hydrates and
 * the System Map renders.
 */

export type Lane = "capture" | "storage" | "meaning" | "outward";

export const LANE_ORDER: Lane[] = ["capture", "storage", "meaning", "outward"];

export const LANE_META: Record<Lane, { label: string; accent: Accent; glyph: string; blurb: string }> = {
  capture: { label: "Capture", accent: "orange", glyph: "↓", blurb: "Where data enters compound." },
  storage: { label: "Storage", accent: "blue", glyph: "▤", blurb: "Where raw + authority live. Bulk stays outside the core." },
  meaning: { label: "Meaning", accent: "violet", glyph: "✦", blurb: "The compound core: evidence → claim → capability." },
  outward: { label: "Outward", accent: "green", glyph: "↑", blurb: "Where compound's meaning becomes value." },
};

export type SystemStatus = "live" | "intake-only" | "reference-only" | "planned";

export const STATUS_META: Record<SystemStatus, { label: string; tone: Accent; dashed: boolean }> = {
  live: { label: "live", tone: "green", dashed: false },
  "intake-only": { label: "intake-only", tone: "orange", dashed: true },
  "reference-only": { label: "reference", tone: "gray", dashed: true },
  planned: { label: "planned", tone: "blue", dashed: true },
};

export type ProjectionKind =
  | "capability-authority"
  | "product-authority"
  | "route-authority"
  | "ledger-stats"
  | "knowledge-sources"
  | "external-intake-map"
  | "job-roster"
  | "static";

/** A declarative topology entry (base + append). */
export type TopologyEntry = {
  id: string;
  name: string;
  description: string;
  lane: Lane;
  accent: Accent;
  glyph: string;
  status: SystemStatus;
  /** extrepo review id, if intake/reference-only. */
  reviewId?: string;
  /** Where the loader finds the live volume + evidence. */
  projection: ProjectionKind;
};

/** A data-flow edge between systems. */
export type TopologyEdge = {
  from: string;
  to: string;
  label: string;
  status: "live" | "planned";
};

/** A hydrated node ready to render (entry + live numbers). */
export type SystemNode = TopologyEntry & {
  volume?: string;
  evidenceRefs?: string[];
};

export type TopologySnapshot = {
  nodes: SystemNode[];
  edges: TopologyEdge[];
  stats: Record<Lane, { live: number; notLive: number }>;
  generatedAt: string;
};
