import Link from "next/link";

import { ForAgents } from "@/components/settings/primitives";
import type { CoordinationView } from "@/lib/coordination-model";

import { OperatorCoordinationList } from "./OperatorCoordinationList";
import { summarizeWaiting } from "./waiting";

/**
 * Who waits on what, in one honest paragraph: how many agent sessions wait,
 * what most of them wait for in plain words, and the sessions that hold
 * work, each linked to its work item. The recorded coordination entries and
 * the full wait list are a technical record for agents, folded shut. Nothing
 * here asks the operator to act: the console has no way to give the
 * confirmation the sessions wait for.
 */
export function WaitingPanel({ view }: { view: CoordinationView }) {
  if (!view.present) {
    return (
      <p className="rounded-lg border border-seam-line bg-seam-soft px-4 py-3 text-sm text-seam">
        The installed Compound tool does not report who waits on whom, so this page cannot show it.
      </p>
    );
  }
  const summary = summarizeWaiting(view);
  const others = summary.reasons.slice(1);
  return (
    <div className="flex flex-col gap-4">
      <div className="max-w-read text-base leading-relaxed text-ink">
        <p>{summary.sentence}</p>
        {others.length ? (
          <p className="mt-1 text-sm text-muted">
            The rest wait for {others.map((reason) => `${reason.words} (${reason.count.toLocaleString("en-US")})`).join("; ")}.
          </p>
        ) : null}
        <p className="mt-1 text-sm text-muted">
          Nothing in this console can give what they wait for; the agent that owns each session handles it.
        </p>
      </div>

      <section className="flex flex-col gap-2">
        <h2 className="section-label">Agents whose sessions hold work</h2>
        {summary.agents.length ? (
          <ul className="flex flex-col divide-y divide-hair rounded-xl border border-hair bg-panel">
            {summary.agents.map((agent) => (
              <li key={agent.key} className="px-4 py-2.5">
                <div className="flex flex-wrap items-baseline justify-between gap-x-3">
                  <Link href={agent.href} className="text-sm font-semibold text-ink hover:underline">{agent.name}</Link>
                  <span className="text-2xs text-muted">{agent.holders.length.toLocaleString("en-US")} {agent.holders.length === 1 ? "session holds" : "sessions hold"} work</span>
                </div>
                <details className="mt-1 text-2xs text-muted">
                  <summary className="cursor-pointer">Its sessions and their work</summary>
                  <ul className="mt-1 flex flex-col gap-1">
                    {agent.holders.map((holder) => (
                      <li key={`${holder.session}:${holder.workId}`}>
                        <Link href={holder.href} className="text-ink hover:underline">{holder.title}</Link>
                        <span> · {holder.stateLabel}{holder.waits ? "" : " · can go on"}</span>
                        <span className="mono text-faint"> · {holder.session}</span>
                      </li>
                    ))}
                  </ul>
                </details>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-sm text-muted">No waiting session holds a work item.</p>
        )}
      </section>

      <ForAgents label="For agents: technical record">
        <OperatorCoordinationList projection={view.operatorCoordination} />
        <p className="text-2xs text-faint">
          {view.waits.length.toLocaleString("en-US")} recorded waits across {summary.sessions.toLocaleString("en-US")} sessions; read the full list with the command below.
        </p>
      </ForAgents>
      <ForAgents cmds={["compound coordination --json"]} label="For agents: command" />
    </div>
  );
}
