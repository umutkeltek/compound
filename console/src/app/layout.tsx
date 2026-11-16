import type { Metadata } from "next";
import "./globals.css";
import { NavContainer } from "@/components/NavContainer";
import { CommandK } from "@/components/CommandK";
import { RefreshControl } from "@/components/RefreshControl";
import { SystemBanner } from "@/components/SystemBanner";
import { AskCompound } from "@/components/AskCompound";
import { ExperienceBar } from "@/components/ExperienceBar";
import { OperatorConnection } from "@/components/OperatorConnection";
import { Breadcrumb, ThemeToggle } from "@/components/kit";

export const metadata: Metadata = {
  title: "Compound",
  description: "Operator workspace over Compound projections",
};

/**
 * No-flash theme resolution (DESIGN.md §6): runs synchronously at the top of
 * <body>, before content paints, so the persisted theme is on
 * <html data-theme> from the first frame. Mirrors ThemeToggle's key +
 * resolution exactly. Light is the default; "auto" and "dark" are opt-in.
 * `suppressHydrationWarning` on <html> covers the attribute this script stamps.
 */
const THEME_SCRIPT = `(function(){try{var k='compound-theme';var p=localStorage.getItem(k)||'light';var d=(p==='dark')||(p==='auto'&&window.matchMedia('(prefers-color-scheme: dark)').matches);document.documentElement.setAttribute('data-theme',d?'dark':'light');var q=localStorage.getItem('compound-experience-depth')||'understand';document.documentElement.setAttribute('data-depth',q);}catch(e){}})();`;

/**
 * The workspace shell: a quiet frame holding the sidebar, and one white panel
 * holding the page. Identity, autonomy and freshness live in the sidebar's
 * status card; the panel header keeps only "where am I" and the refresh and
 * theme controls. Ask Compound floats at the bottom; ⌘K opens from the
 * sidebar search.
 */
export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" suppressHydrationWarning>
      <body>
        <script dangerouslySetInnerHTML={{ __html: THEME_SCRIPT }} />
        <div className="flex min-h-screen">
          <NavContainer variant="desktop" />
          <div className="flex min-w-0 flex-1 flex-col md:py-3 md:pr-3">
            <div className="shell-panel flex min-w-0 flex-1 flex-col">
              <header className="sticky top-0 z-40 flex min-h-12 flex-wrap items-center gap-2 border-b border-hair bg-panel/85 px-3 py-2 backdrop-blur sm:flex-nowrap sm:gap-3 sm:px-5 md:rounded-t-2xl md:px-8 md:py-3">
                <NavContainer variant="mobile" />
                <div className="min-w-0"><Breadcrumb /></div>
                <div className="hidden min-w-0 2xl:block"><SystemBanner /></div>
                <div className="ml-auto flex shrink-0 items-center gap-1 sm:gap-2">
                  <OperatorConnection />
                  <RefreshControl responsive />
                  <ThemeToggle responsive />
                </div>
              </header>
              <div className="2xl:hidden"><SystemBanner compact /></div>
              <ExperienceBar />
              <main className="mx-auto w-full flex-1 px-4 pb-24 pt-6 sm:px-6 md:px-8 md:pt-8" style={{ maxWidth: "1480px" }}>
                {children}
              </main>
            </div>
          </div>
        </div>
        <AskCompound />
        <CommandK />
      </body>
    </html>
  );
}
