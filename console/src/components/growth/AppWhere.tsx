import { ReadNotice, type ReadView } from "@/components/kit/ReadState";
import type { AppWhereModel } from "@/components/growth/app-where";

function age(days: number | null): string {
  if (days === null) return "when it was observed is not reported";
  if (days === 0) return "observed today";
  return `observed ${days} ${days === 1 ? "day" : "days"} ago`;
}

/**
 * "Where it lives and runs": the app's code, hosts and the services it calls,
 * each marked observed or declared, with how old the observation is. A graph
 * that has not been read says so; an app with no links says that this does
 * not prove it has none.
 */
export function AppWhere({ name, model, read }: { name: string; model: AppWhereModel; read: ReadView }) {
  return (
    <section id="app-where" className="flex scroll-mt-24 flex-col gap-2" aria-labelledby="app-where-title">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h2 id="app-where-title" className="text-lg font-semibold tracking-tight text-ink">Where it lives and runs</h2>
        {model.links ? (
          <span className={`text-xs ${model.state === "stale" ? "text-seam" : "text-faint"}`}>
            Inventory {age(model.observedDaysAgo)}{model.state === "stale" ? ", out of date" : ""}
          </span>
        ) : null}
      </div>
      {model.state === "missing" ? (
        <p className="text-sm text-muted">No inventory of code and hosts is recorded, so where {name} runs is not known.</p>
      ) : !model.links ? (
        <ReadNotice read={read} label="where each app's code and hosts are" />
      ) : model.links.length ? (
        <div className="overflow-x-auto rounded-xl border border-hair bg-panel">
          <table className="w-full text-left text-sm">
            <caption className="sr-only">Where {name}&apos;s code is, where it runs and what it calls</caption>
            <tbody>
              {model.links.map((link, i) => (
                <tr key={i} className="border-t border-hair first:border-t-0">
                  <th scope="row" className="w-32 px-4 py-2.5 align-top text-xs font-medium text-muted">{link.label}</th>
                  <td className="px-4 py-2.5 align-top text-ink">
                    <span className="break-words">{link.target}</span>
                    {link.evidence ? (
                      <details className="mt-0.5 text-xs text-faint">
                        <summary className="cursor-pointer">How we know</summary>
                        <p className="mt-1 max-w-read break-words">{link.evidence}</p>
                      </details>
                    ) : null}
                  </td>
                  <td className="w-40 px-4 py-2.5 align-top text-xs text-muted">
                    {link.confidence === "observed" ? "Seen in the code or on the machine" : link.confidence === "declared" ? "Written down, not seen" : link.confidence}
                    {link.carried ? <span className="block text-faint">kept from an earlier read</span> : null}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <p className="text-sm text-muted">No code, host or service is linked to {name} in the inventory. That does not prove it has none.</p>
      )}
      {model.links && model.other.length ? (
        <p className="text-xs text-faint">Also linked: {model.other.map((o) => `${o.count} ${o.relation}`).join(", ")}.</p>
      ) : null}
    </section>
  );
}
