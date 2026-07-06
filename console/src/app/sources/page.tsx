import { CopyCmd } from "@/components/CopyCmd";
import { Card, Chip, EmptyState, FreshnessChip, Stat, type Accent } from "@/components/ui";
import { ago, arr, getSourceConnections, str, type Json } from "@/lib/compound";

export const dynamic = "force-dynamic";

function statusTone(status: string): Accent {
  switch (status) {
    case "connected":
    case "observed":
      return "green";
    case "blocked_private_config":
    case "needs_private_input":
    case "ready_preflight":
      return "orange";
    case "needs_review":
    case "needs_mapping":
      return "violet";
    case "stale":
      return "red";
    default:
      return "gray";
  }
}

function statusLabel(status: string): string {
  switch (status) {
    case "blocked_private_config":
      return "needs private config";
    case "needs_private_input":
      return "needs private input";
    case "ready_preflight":
      return "ready for preflight";
    case "needs_review":
      return "needs review";
    case "needs_mapping":
      return "needs mapping";
    case "not_connected":
      return "not connected";
    default:
      return status.replace(/_/g, " ") || "unknown";
  }
}

function shellArg(arg: string): string {
  if (/^[A-Za-z0-9_./:=@+-]+$/.test(arg)) return arg;
  return JSON.stringify(arg);
}

function commandText(command: unknown): string {
  return arr(command).map(String).filter(Boolean).map(shellArg).join(" ");
}

function SourceActionCard({ card }: { card: Json }) {
  const command = commandText(card.command);
  const inputs = arr(card.required_inputs).map(String).filter(Boolean);
  const validation = arr(card.expected_validation).map(String).filter(Boolean);
  const stops = arr(card.stop_conditions).map(String).filter(Boolean);

  return (
    <li className="border-t border-line pt-3 first:border-t-0 first:pt-0">
      <div className="flex flex-wrap items-center gap-1.5">
        <Chip tone={statusTone(str(card.severity) === "high" ? "needs_private_input" : "needs_review")}>
          {str(card.severity) || "review"}
        </Chip>
        {str(card.kind) ? <Chip tone="gray">{str(card.kind).replace(/_/g, " ")}</Chip> : null}
      </div>
      <div className="mt-2 text-[12.5px] font-medium leading-snug text-ink">
        {str(card.title) || "Source action"}
      </div>
      {inputs.length ? (
        <p className="mt-1 text-[11.5px] leading-relaxed text-ink-3">Needs: {inputs.join("; ")}</p>
      ) : null}
      {validation.length ? (
        <p className="mt-1 text-[11.5px] leading-relaxed text-ink-3">
          Good result: {validation.slice(0, 2).join("; ")}
        </p>
      ) : null}
      {stops.length ? (
        <p className="mt-1 text-[11.5px] leading-relaxed text-ink-3">Stop if: {stops.slice(0, 2).join("; ")}</p>
      ) : null}
      {command ? (
        <div className="mt-2">
          <CopyCmd cmd={command} compact />
        </div>
      ) : null}
    </li>
  );
}

function SourceConnectionCard({ connection }: { connection: Json }) {
  const status = str(connection.status);
  const sourceKinds = arr(connection.source_kinds).map(String).filter(Boolean);
  const produces = arr(connection.produces).map(String).filter(Boolean);
  const cards = arr(connection.latest_cards);
  const lastSeen = str(connection.last_seen);

  return (
    <Card
      title={str(connection.label) || str(connection.id)}
      accent={statusTone(status)}
      glyph="S"
      right={<Chip tone={statusTone(status)}>{statusLabel(status)}</Chip>}
    >
      <div className="flex flex-col gap-3">
        <div className="flex flex-wrap gap-1.5">
          {sourceKinds.slice(0, 4).map((kind) => (
            <Chip key={kind} tone="gray">
              {kind}
            </Chip>
          ))}
          {connection.private_setup_needed ? <Chip tone="orange">private setup</Chip> : null}
          {connection.row_count ? <Chip tone="blue">{connection.row_count} rows</Chip> : null}
          {lastSeen ? <Chip tone="gray">{ago(lastSeen) || lastSeen}</Chip> : null}
        </div>

        {produces.length ? (
          <div>
            <div className="section-label mb-1">Produces</div>
            <div className="flex flex-wrap gap-1.5">
              {produces.slice(0, 5).map((item) => (
                <Chip key={item} tone="green">
                  {item}
                </Chip>
              ))}
            </div>
          </div>
        ) : null}

        <div>
          <div className="section-label mb-1">Next safe action</div>
          <p className="text-[12.5px] leading-relaxed text-ink-2">
            {str(connection.next_safe_action) || "No next action surfaced."}
          </p>
        </div>

        {str(connection.source_ref) ? (
          <code className="mono break-all text-[10.5px] leading-relaxed text-ink-3">{str(connection.source_ref)}</code>
        ) : null}

        {cards.length ? (
          <div>
            <div className="section-label mb-2">Latest cards</div>
            <ul className="flex flex-col gap-3">
              {cards.map((card) => (
                <SourceActionCard key={str(card.id) || str(card.kind)} card={card} />
              ))}
            </ul>
          </div>
        ) : null}
      </div>
    </Card>
  );
}

export default async function SourcesPage() {
  const projection = await getSourceConnections();
  const connections = arr(projection?.connections);
  const counts = (projection?.counts ?? {}) as Json;

  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-end justify-between gap-4">
        <div>
          <h1 className="text-[22px] font-semibold tracking-tight">Sources</h1>
          <p className="mt-0.5 text-[12.5px] text-ink-3">
            What arrived, what is connected, and what needs input before import.
          </p>
        </div>
        <FreshnessChip generatedAt={str(projection?.generated_at)} source="compound source connections --json" />
      </div>

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Stat label="Sources" value={Number(counts.total) || connections.length} glyph="S" accent="blue" />
        <Stat label="Connected" value={Number(counts.connected) || 0} glyph=">" accent="green" />
        <Stat label="Needs input" value={Number(counts.needs_input) || 0} glyph="!" accent="orange" />
        <Stat label="Cards" value={Number(counts.action_required) || 0} glyph="C" accent="violet" />
      </div>

      {connections.length ? (
        <div className="grid gap-4 lg:grid-cols-2">
          {connections.map((connection) => (
            <SourceConnectionCard key={str(connection.id)} connection={connection} />
          ))}
        </div>
      ) : (
        <EmptyState>No source connections projection available.</EmptyState>
      )}
    </div>
  );
}
