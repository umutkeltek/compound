"use client";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { navItemForPath } from "@/lib/experience";
import { ChevronRightIcon, GridIcon } from "@/components/kit/icons";

/**
 * Panel-top breadcrumb: "Workspace › Page › item". The page name comes from the
 * same sidebar registry the nav renders, so the two can never disagree. A
 * deeper segment (an app slug or work id) renders as the tail; ids stay mono.
 */
function humanizeSlug(slug: string): string {
  return decodeURIComponent(slug)
    .split(/[-_]/)
    .filter(Boolean)
    .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
    .join(" ");
}

export function Breadcrumb() {
  const pathname = usePathname() || "/";
  const segments = pathname.split("/").filter(Boolean);
  const item = navItemForPath(pathname);
  const section = item?.label ?? (segments[0] ? humanizeSlug(segments[0]) : "Today");
  const sectionHref = item?.href ?? `/${segments[0] ?? ""}`;
  const tail = segments[1];
  const tailIsId = tail ? /^(work|ctx|rec|tr|cand|ev)_/.test(tail) : false;

  return (
    <nav aria-label="Breadcrumb" className="flex min-w-0 items-center gap-1.5 text-sm text-faint">
      <Link href="/" className="inline-flex items-center gap-1.5 transition-colors hover:text-muted">
        <GridIcon size={13} />
        Workspace
      </Link>
      <span aria-hidden><ChevronRightIcon size={12} /></span>
      {tail ? (
        <>
          <Link href={sectionHref} className="transition-colors hover:text-muted">
            {section}
          </Link>
          <span aria-hidden><ChevronRightIcon size={12} /></span>
          <span className={`max-w-56 truncate text-ink ${tailIsId ? "mono text-xs" : "font-medium"}`} title={tail}>
            {tailIsId ? tail : humanizeSlug(tail)}
          </span>
        </>
      ) : (
        <span className="font-medium text-ink">{section}</span>
      )}
    </nav>
  );
}
