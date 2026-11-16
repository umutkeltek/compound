"use client";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useSyncExternalStore } from "react";
import { placeForPath, sectionHref } from "@/lib/experience";
import { ChevronRightIcon } from "@/components/kit/icons";

/**
 * Panel-top breadcrumb: section › page › item, from the same registry the
 * sidebar and tabs read (lib/experience.ts), so the names cannot disagree.
 * The page is left out when it has the section's own name (Today, Work,
 * Settings). A deeper segment (an app slug or a work id) is the item; ids stay
 * mono, unless the page names itself with <BreadcrumbTitle> (a work item's
 * title, for example). An address the console does not have says so instead
 * of inventing a page name. The browser tab's title reads the same crumbs,
 * most specific first, so no two tabs are all called "Compound".
 */
function humanizeSlug(slug: string): string {
  let text = slug;
  try {
    text = decodeURIComponent(slug);
  } catch {
    // keep the raw segment
  }
  return text
    .split(/[-_]/)
    .filter(Boolean)
    .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
    .join(" ");
}

export type Crumb = { label: string; href?: string; id?: boolean };

/* An item page's own title for its crumb, by address (set by BreadcrumbTitle). */
const detailTitles = new Map<string, string>();
const titleListeners = new Set<() => void>();
function setDetailTitle(pathname: string, title: string | null) {
  if (title) detailTitles.set(pathname, title);
  else detailTitles.delete(pathname);
  titleListeners.forEach((listener) => listener());
}
function subscribeTitles(listener: () => void) {
  titleListeners.add(listener);
  return () => titleListeners.delete(listener);
}

/** Rendered by an item page (PageHeader does it for a text title): its crumb and tab title use this name. */
export function BreadcrumbTitle({ title }: { title: string }) {
  const pathname = usePathname() || "/";
  useEffect(() => {
    setDetailTitle(pathname, title);
    return () => setDetailTitle(pathname, null);
  }, [pathname, title]);
  return null;
}

/** The browser tab title for these crumbs: most specific first, then "Compound". */
export function documentTitleFor(crumbs: readonly Crumb[]): string {
  return [...crumbs].reverse().map((crumb) => crumb.label).concat("Compound").join(" · ");
}

/** The crumbs for an address. Pure, so the shell check can read it without a browser. */
export function breadcrumbFor(pathname: string, itemTitle = ""): Crumb[] {
  const place = placeForPath(pathname);
  if (!place) return [{ label: "Page not found" }];
  const { section, page } = place;
  const tail = pathname === page.href ? "" : (pathname.split("/").filter(Boolean).pop() ?? "");
  const crumbs: Crumb[] = [{ label: section.label, href: sectionHref(section) }];
  if (page.label !== section.label) crumbs.push({ label: page.label, href: page.href });
  if (tail) {
    const id = /^(work|ctx|rec|tr|cand|ev)_/.test(tail);
    if (itemTitle) crumbs.push({ label: itemTitle });
    else crumbs.push({ label: id ? tail : humanizeSlug(tail), id });
  }
  const last = crumbs[crumbs.length - 1];
  crumbs[crumbs.length - 1] = { label: last.label, id: last.id };
  return crumbs;
}

export function Breadcrumb() {
  const pathname = usePathname() || "/";
  const itemTitle = useSyncExternalStore(subscribeTitles, () => detailTitles.get(pathname) ?? "", () => "");
  const crumbs = breadcrumbFor(pathname, itemTitle);
  return (
    <nav aria-label="Breadcrumb" className="flex min-w-0 items-center gap-1.5 text-sm text-faint">
      <title>{documentTitleFor(crumbs)}</title>
      {crumbs.map((crumb, index) => (
        <span key={`${index}-${crumb.label}`} className="flex min-w-0 items-center gap-1.5">
          {index > 0 ? <span aria-hidden><ChevronRightIcon size={12} /></span> : null}
          {crumb.href ? (
            <Link href={crumb.href} className="shrink-0 transition-colors hover:text-muted">
              {crumb.label}
            </Link>
          ) : (
            <span
              aria-current="page"
              className={`max-w-56 truncate text-ink ${crumb.id ? "mono text-xs" : "font-medium"}`}
              title={crumb.label}
            >
              {crumb.label}
            </span>
          )}
        </span>
      ))}
    </nav>
  );
}
