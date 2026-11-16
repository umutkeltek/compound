"use client";
import Link from "next/link";
import { usePathname } from "next/navigation";
import {
  BACK_ROOM_LABEL,
  NAV_SECTIONS,
  placeForPath,
  sectionTabs,
  type NavPage,
  type NavSectionId,
} from "@/lib/experience";

/**
 * The tab row under every page header: the pages of the section this page
 * belongs to (lib/experience.ts), so each stays one click away while the
 * sidebar lists only sections. The address decides the section; `fallback`
 * (the section of the header's `name`) covers a render with no address.
 * A one-page section shows no row. Pages agents use sit in a quieter
 * "Back room" cluster at the end.
 */
export function NavTabs({ fallback }: { fallback?: NavSectionId }) {
  const pathname = usePathname();
  const place = pathname ? placeForPath(pathname) : null;
  const section = place?.section ?? NAV_SECTIONS.find((s) => s.id === fallback) ?? null;
  if (!section) return null;
  const { main, backRoom } = sectionTabs(section);
  if (!main.length && !backRoom.length) return null;
  const activePage = place?.section === section ? place.page : null;

  const tab = (page: NavPage, quiet: boolean) => {
    const active = page === activePage;
    return (
      <Link
        key={page.href}
        href={page.href}
        aria-current={active ? "page" : undefined}
        data-active={active}
        title={page.description}
        className={`-mb-px inline-flex min-h-9 shrink-0 items-center whitespace-nowrap border-b-2 px-1 pb-2 pt-1 transition-colors ${
          active
            ? "border-ink font-semibold text-ink"
            : `border-transparent hover:border-hair hover:text-ink ${quiet ? "text-faint" : "text-muted"}`
        } ${quiet ? "text-xs" : "text-sm"}`}
      >
        {page.label}
      </Link>
    );
  };

  return (
    <nav
      aria-label={`${section.label} pages`}
      data-section-tabs={section.id}
      className="flex min-w-0 flex-wrap items-end gap-x-5 gap-y-1 border-b border-hair"
    >
      {main.map((page) => tab(page, false))}
      {backRoom.length ? (
        <span className="ml-auto flex flex-wrap items-end gap-x-4" data-back-room>
          <span className="pb-2 pt-1 text-2xs font-medium text-faint" title="Pages agents use; you rarely need them.">
            {BACK_ROOM_LABEL}
          </span>
          {backRoom.map((page) => tab(page, true))}
        </span>
      ) : null}
    </nav>
  );
}
