import { PageHeader } from "@/components/kit";
import { workListHref } from "@/lib/links";

/**
 * A /work/<id> address that did not open a work item. Either no work item has
 * this id, or Compound could not be read just now; the page cannot tell which,
 * so it says both and offers the list.
 */
export default function WorkNotFound() {
  return (
    <div className="flex flex-col gap-6" data-not-found="work">
      <PageHeader
        name="Work"
        title="No work item opened at this address"
        lead="Either no work item has this id, or Compound could not be read just now. Reload to try again, or find it in the work list."
        action={{ label: "See all work", href: workListHref({ lens: "table", filter: "all" }) }}
      />
    </div>
  );
}
