import { redirect } from "next/navigation";

import { workListHref } from "@/lib/links";

/** Compatibility route for direct rendering environments that skip redirects(): the Flow view is retired, so it opens the board. */
export default function FlowCompatibilityPage() {
  redirect(workListHref());
}
