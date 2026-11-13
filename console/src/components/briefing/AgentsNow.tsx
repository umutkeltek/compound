import Link from "next/link";

import { ReadNotice, type ReadView } from "@/components/kit";
import { describeAgents, describeInProgress, type AgentCount, type InProgressCount } from "@/lib/counts";

/**
 * "What the agents are doing": who holds work, how much is marked in
 * progress, and how much of that has been open for more than a week. When
 * nothing needs the operator this is Today's main answer (`prominent`);
 * otherwise it sits below the decisions, quieter. Counts that have not been
 * read show the read state, never 0.
 */
export function AgentsNow({
  agents,
  agentsRead,
  inProgress,
  workRead,
  prominent = false,
}: {
  agents: AgentCount | null;
  agentsRead: ReadView;
  inProgress: InProgressCount | null;
  workRead: ReadView;
  prominent?: boolean;
}) {
  const progress = inProgress ? describeInProgress(inProgress) : null;
  return (
    <section
      aria-label="What the agents are doing"
      className={`rounded-xl border border-hair px-5 py-4 ${prominent ? "bg-panel shadow-sm" : "bg-soft"}`}
    >
      <h2 className={`${prominent ? "text-md text-strong" : "text-sm text-ink"} font-semibold`}>What the agents are doing</h2>
      <div className="mt-2 flex flex-col gap-1.5 text-sm leading-relaxed text-muted">
        {agents ? <p>{describeAgents(agents)}</p> : <ReadNotice read={agentsRead} label="the agents" />}
        {progress ? (
          <p>
            <span className="text-ink">{progress.headline}</span>
            {progress.detail ? <>: {progress.detail}</> : "."}
            {progress.age ? <span className="block text-seam">{progress.age}</span> : null}
          </p>
        ) : (
          <ReadNotice read={workRead} label="open work" />
        )}
      </div>
      <div className="mt-3 flex flex-wrap gap-x-4 gap-y-1 text-xs font-semibold">
        <Link href="/live" className="text-work hover:underline">See who works on what →</Link>
        <Link href="/work" className="text-work hover:underline">Open all work →</Link>
      </div>
    </section>
  );
}
