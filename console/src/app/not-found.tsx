import Link from "next/link";
import { PageHeader } from "@/components/kit";
import { appHref, workListHref } from "@/lib/links";

/**
 * Any address the console does not have. It says so plainly inside the
 * normal frame (sidebar, top bar, theme), and leads back to the lists.
 */
export default function NotFound() {
  return (
    <div className="flex flex-col gap-6" data-not-found="page">
      <PageHeader
        title="There is no page at this address"
        lead="The link may be old or mistyped. Every page is in the menu on the left, and Search finds pages, apps, decisions and work by name."
        action={{ label: "Go to Today", href: "/" }}
      />
      <nav aria-label="Lists" className="flex flex-wrap gap-x-5 gap-y-2 text-sm font-semibold text-work">
        <Link href={appHref("")} className="hover:underline">All apps →</Link>
        <Link href={workListHref()} className="hover:underline">Work →</Link>
        <Link href="/reviews" className="hover:underline">All decisions →</Link>
      </nav>
    </div>
  );
}
