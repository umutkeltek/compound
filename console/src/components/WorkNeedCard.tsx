import Link from "next/link";

import { CopyCmd } from "@/components/CopyCmd";
import { Chip, StateBadge } from "@/components/ui";
import { arr, str, type Json } from "@/lib/compound";
import { shortText } from "@/lib/work-data";

function shellArg(arg: string): string {
  if (/^[A-Za-z0-9_./:=@+-]+$/.test(arg)) return arg;
  return JSON.stringify(arg);
}

function commandText(command: unknown): string {
  const parts = arr(command).map(String).filter(Boolean);
  return parts.map(shellArg).join(" ");
}

function cardCopy(card: Json, work: Json): { label: string; question: string; body: string; chip: string } {
  const kind = str(card.kind);
  const workId = str(card.work_id) || str(work.work_id) || str(work.id);

  if (kind === "source_private_config_gate") {
    return {
      label: "Private setup check",
      question: "Can Compound run the read-only ASC preflight?",
      body:
        "This is the App Store Connect daily signals task. It is parked because the next useful step needs your private product mapping config, and Compound must not fetch live ASC data or load the scheduler without you.",
      chip: "read-only",
    };
  }

  if (kind === "memory_candidate_adjudication") {
    const candidateId = str(card.candidate_id);
    return {
      label: "Memory review",
      question: candidateId ? `Should ${candidateId} become memory?` : "Should this candidate become memory?",
      body: `This is the small-batch conversation memory task${workId ? ` for ${workId}` : ""}. Compound found one candidate and panel evidence, but promotion stays parked until you decide.`,
      chip: "no auto-promotion",
    };
  }

  return {
    label: "Decision needed",
    question: str(card.title) || str(work.next_action) || "Compound needs your judgment.",
    body: shortText(str(card.boundary) || str(work.next_action), 260),
    chip: "operator-only",
  };
}

function TinyList({ title, items }: { title: string; items: string[] }) {
  if (!items.length) return null;
  return (
    <details className="group">
      <summary className="cursor-pointer text-[11px] font-medium text-ink-3 hover:text-ink-2">
        {title} · {items.length}
      </summary>
      <ul className="mt-1.5 flex flex-col gap-1">
        {items.slice(0, 5).map((item, index) => (
          <li key={index} className="text-[11.5px] leading-relaxed text-ink-3">
            {item}
          </li>
        ))}
      </ul>
    </details>
  );
}

export function WorkNeedCard({ work, card }: { work: Json; card: Json }) {
  const workId = str(card.work_id) || str(work.work_id) || str(work.id);
  const state = str(work.state);
  const copy = cardCopy(card, work);
  const command = commandText(card.command);
  const stopConditions = arr(card.stop_conditions).map(String).filter(Boolean);
  const validations = arr(card.expected_validation).map(String).filter(Boolean);
  const inputs = arr(card.required_inputs).map(String).filter(Boolean);
  const proof = str(card.proof);

  return (
    <div className="rounded-xl border border-line bg-white px-4 py-3.5">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-1.5">
            <Chip tone="orange">{copy.label}</Chip>
            {copy.chip ? <Chip tone="gray">{copy.chip}</Chip> : null}
            {state ? <StateBadge state={state} /> : null}
          </div>
          <h3 className="mt-2 text-[13.5px] font-semibold leading-snug text-ink">{copy.question}</h3>
        </div>
        {workId ? (
          <Link href={`/work/${workId}`} className="mono shrink-0 text-[10.5px] text-ink-3 hover:underline">
            {workId}
          </Link>
        ) : null}
      </div>

      <p className="mt-2 text-[12.5px] leading-relaxed text-ink-2">{copy.body}</p>

      {command ? (
        <div className="mt-3 rounded-lg bg-slate-50 px-3 py-2">
          <div className="section-label mb-1">Safest next step</div>
          <CopyCmd cmd={command} compact={false} />
        </div>
      ) : null}

      <div className="mt-3 grid gap-2">
        <TinyList title="Needs from you" items={inputs} />
        <TinyList title="Good result looks like" items={validations} />
        <TinyList title="Stop if" items={stopConditions} />
      </div>

      {proof ? (
        <p className="mt-3 rounded-lg bg-canvas px-3 py-2 text-[11.5px] leading-relaxed text-ink-3">
          {proof}
        </p>
      ) : null}
    </div>
  );
}
