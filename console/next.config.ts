import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  devIndicators: false,
  // Retired routes → permanent redirects (308) so bookmarks / old nav / stale
  // links still land somewhere honest.
  //   /flow        → Work's default Pipeline lens.
  //   /map/memory  → the memory ego-graph now lives embedded in /memory.
  async redirects() {
    return [
      { source: "/flow", destination: "/work", permanent: true },
      { source: "/map/memory", destination: "/memory", permanent: true },
    ];
  },
};

export default nextConfig;
