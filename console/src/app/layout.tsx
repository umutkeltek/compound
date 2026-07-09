import type { Metadata } from "next";
import "./globals.css";
import { NavContainer } from "@/components/NavContainer";
import { CommandK } from "@/components/CommandK";
import { RefreshControl } from "@/components/RefreshControl";
import { SystemBanner } from "@/components/SystemBanner";

export const metadata: Metadata = {
  title: "compound console",
  description: "Operator control surface over compound projections",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>
        <div className="flex min-h-screen">
          <NavContainer />
          <div className="flex min-w-0 flex-1 flex-col">
            <header className="sticky top-0 z-40 flex items-center justify-end gap-2 border-b border-line bg-canvas/80 px-6 py-2.5 backdrop-blur">
              <SystemBanner />
              <RefreshControl />
              <CommandK />
            </header>
            <main className="mx-auto w-full max-w-[1200px] flex-1 px-6 py-6">{children}</main>
          </div>
        </div>
      </body>
    </html>
  );
}
