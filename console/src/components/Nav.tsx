"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

type NavLink = { label: string; href?: string; soon?: boolean };
type Group = { label: string; links: NavLink[] };

const GROUPS: Group[] = [
  { label: "Overview", links: [{ label: "Dashboard", href: "/" }] },
  { label: "Work", links: [{ label: "Board", href: "/work" }, { label: "Executors", href: "/executors" }] },
  {
    label: "Meaning",
    links: [
      { label: "Reviews", href: "/reviews" },
      { label: "Memory", href: "/memory" },
      { label: "Loops", href: "/loops" },
      { label: "Sources", href: "/sources" },
    ],
  },
  {
    label: "System",
    links: [
      { label: "Map", href: "/map" },
      { label: "Capability", href: "/map?lens=registry" },
      { label: "Cost", soon: true },
    ],
  },
];

function Dot({ active }: { active: boolean }) {
  return (
    <span
      className="h-[7px] w-[7px] rounded-[2.5px]"
      style={{ background: active ? "#4f46e5" : "#c3cad4" }}
    />
  );
}

export function Nav() {
  const pathname = usePathname();
  return (
    <aside className="sticky top-0 flex h-screen w-[218px] shrink-0 flex-col gap-6 border-r border-line bg-white/70 px-4 py-5 backdrop-blur">
      <div className="flex items-center gap-2.5 px-1">
        <span className="flex h-8 w-8 items-center justify-center rounded-[10px] bg-ink text-[15px] font-bold text-white">
          c
        </span>
        <div className="leading-tight">
          <div className="text-[13.5px] font-semibold">compound</div>
          <div className="text-[11px] text-ink-3">console</div>
        </div>
      </div>

      <nav className="flex flex-1 flex-col gap-5">
        {GROUPS.map((g) => (
          <div key={g.label}>
            <div className="section-label mb-1.5 px-2">{g.label}</div>
            <div className="flex flex-col gap-0.5">
              {g.links.map((l) =>
                l.href ? (
                  <Link
                    key={l.label}
                    href={l.href}
                    className="nav-item"
                    data-active={pathname === l.href}
                  >
                    <Dot active={pathname === l.href} />
                    {l.label}
                  </Link>
                ) : (
                  <span
                    key={l.label}
                    className="nav-item"
                    data-soon="true"
                    title="Appends when its projection lands (base + append)"
                  >
                    <Dot active={false} />
                    {l.label}
                    <span className="ml-auto text-[10px] text-ink-3">soon</span>
                  </span>
                ),
              )}
            </div>
          </div>
        ))}
      </nav>

      <div className="px-1 text-[10.5px] leading-relaxed text-ink-3">
        Projection-first.
        <br />
        Continue uses governed local commands in operator mode.
      </div>
    </aside>
  );
}
