"use client";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { placeForPath, sectionHref } from "@/lib/experience";
import { ChevronRightIcon } from "@/components/kit/icons";

/**
 * Panel-top breadcrumb: section › page › item, from the same registry the
 * sidebar and tabs read (lib/experience.ts), so the names cannot disagree.
 * The page is left out when it has the section's own name (Today, Work,
 * Settings). A deeper segment (an app slug or a work id) is the item; ids stay
 * mono. An address the console does not have says so instead of inventing a
 * page name.
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

/** The crumbs for an address. Pure, so the shell check can read it without a browser. */
export function breadcrumbFor(pathname: string): Crumb[] {
  const place = placeForPath(pathname);
  if (!place) return [{ label: "Page not found" }];
  const { section, page } = place;
  const tail = pathname === page.href ? "" : (pathname.split("/").filter(Boolean).pop() ?? "");
  const crumbs: Crumb[] = [{ label: section.label, href: sectionHref(section) }];
  if (page.label !== section.label) crumbs.push({ label: page.label, href: page.href });
  if (tail) {
    const id = /^(work|ctx|rec|tr|cand|ev)_/.test(tail);
    crumbs.push({ label: id ? tail : humanizeSlug(tail), id });
  }
  const last = crumbs[crumbs.length - 1];
  crumbs[crumbs.length - 1] = { label: last.label, id: last.id };
  return crumbs;
}

export function Breadcrumb() {
  const pathname = usePathname() || "/";
  const crumbs = breadcrumbFor(pathname);
  return (
    <nav aria-label="Breadcrumb" className="flex min-w-0 items-center gap-1.5 text-sm text-faint">
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
