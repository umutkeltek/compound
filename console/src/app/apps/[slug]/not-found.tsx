import { PageHeader } from "@/components/kit";
import { appHref } from "@/lib/links";

/**
 * An /apps/<name> address for an app Compound does not list. The app page
 * shows this only after the app list was read, so "not listed" is a fact.
 */
export default function AppNotFound() {
  return (
    <div className="flex flex-col gap-6" data-not-found="app">
      <PageHeader
        name="All apps"
        title="No app at this address"
        lead="Compound does not list an app under this name. It may have been renamed, or the link is mistyped. Every app is in the list."
        action={{ label: "See all apps", href: appHref("") }}
      />
    </div>
  );
}
