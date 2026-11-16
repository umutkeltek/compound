import Link from "next/link";

import type { AppWorkModel } from "@/components/growth/app-work";

/** A pending verdict about this app that only the owner can give, with the exact decision to open. */
export type AppVerdict = { text: string; href: string };

/**
 * "Needs you": what about this app waits for the owner, first on the page.
 * Work on hold for this app and a real verdict due. While the work read has
 * not landed it says so; it never claims "nothing" before it knows.
 */
export function AppNeedsYou({ name, work, verdicts }: { name: string; work: AppWorkModel | null; verdicts: AppVerdict[] }) {
  const held = work ? work.rows.filter((row) => row.onHold) : [];
  const items = [
    ...held.map((row) => ({ key: row.id, href: row.href, lead: "On hold, waiting for you", text: row.title })),
    ...verdicts.map((v, i) => ({ key: `verdict-${i}`, href: v.href, lead: "Verdict due", text: v.text })),
  ];
  return (
    <section id="app-needs-you" className="flex scroll-mt-24 flex-col gap-2" aria-labelledby="app-needs-you-title">
      <h2 id="app-needs-you-title" className="text-lg font-semibold tracking-tight text-ink">Needs you</h2>
      {items.length ? (
        <ul className="flex flex-col gap-2">
          {items.map((item) => (
            <li key={item.key}>
              <Link href={item.href} className="admission-seam flex flex-col gap-0.5 rounded-xl border border-seam-line bg-panel px-4 py-3 hover:bg-soft">
                <span className="text-xs font-semibold text-seam">{item.lead}</span>
                <span className="line-clamp-2 text-sm font-medium text-ink">{item.text}</span>
              </Link>
            </li>
          ))}
          {work && held.length > 1 ? (
            <li>
              <Link href={work.hrefs.onHold} className="text-sm font-medium text-work hover:underline">
                See the {held.length} items on hold in Work →
              </Link>
            </li>
          ) : null}
        </ul>
      ) : work ? (
        <p className="text-sm text-muted">Nothing about {name} waits for you: no work on hold and no verdict due.</p>
      ) : (
        <p className="text-sm text-muted">Still reading {name}&apos;s work, so it is not yet known whether anything waits for you.</p>
      )}
    </section>
  );
}
