import { redirect } from "next/navigation";

import { projectsRedirectHref } from "@/lib/work-list";

/**
 * The Projects page is retired: its grouping lives in Work ("By app"), and an
 * app's own page is its home. Old addresses keep working: an app's scope goes
 * to that app's page, an opened work item to its own page, anything else to
 * Work grouped by app (lib/work-list.ts `projectsRedirectHref`).
 */
export default async function ProjectsPage({ searchParams }: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  redirect(projectsRedirectHref(await searchParams));
}
