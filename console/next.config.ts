import type { NextConfig } from "next";

const configuredBuildKind = process.env.COMPOUND_CONSOLE_BUILD_KIND;
const buildKind =
  configuredBuildKind === "development" || configuredBuildKind === "production"
    ? configuredBuildKind
    : process.env.NODE_ENV === "development"
      ? "development"
      : "production";

const nextConfig: NextConfig = {
  devIndicators: false,
  // Development and the launchd-served production runtime must never share
  // artifacts. Next dev mutates its tree while running; a shared `.next`
  // allowed that mutation to invalidate the production server's asset map.
  distDir: `.next/${buildKind}`,
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
