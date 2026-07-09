import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  devIndicators: false,
  // /flow is retired — it is now Work's default Pipeline lens. Permanent
  // redirect (308) so any bookmark, the old nav, or a stale link lands on /work.
  async redirects() {
    return [{ source: "/flow", destination: "/work", permanent: true }];
  },
};

export default nextConfig;
