import Link from "next/link";

import { CopyCmd } from "@/components/CopyCmd";
import { DecisionForm } from "@/components/DecisionForm";
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

function numberText(value: unknown): string {
  const n = Number(value);
  if (!Number.isFinite(n)) return "";
  return n.toFixed(2).replace(/0+$/, "").replace(/\.$/, "");
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
    return {
      label: "Memory review",
      question: "Should this become memory?",
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

function MemoryCandidatePreview({ card }: { card: Json }) {
  if (str(card.kind) !== "memory_candidate_adjudication") return null;

  const candidateId = str(card.candidate_id);
  const object = str(card.candidate_object);
  const predicate = str(card.candidate_predicate);
  const entity = str(card.candidate_entity);
  const state = str(card.candidate_state);
  const visibility = str(card.candidate_visibility);
  const sourceRef = str(card.candidate_source_ref);
  const itemRef = str(card.candidate_item_ref);
  const extractionId = str(card.candidate_extraction_id);
  const extractedBy = str(card.candidate_extracted_by);
  const sourceKind = str(card.candidate_source_kind);
  const sourceTitle = str(card.candidate_source_title);
  const sourceExcerpt = str(card.candidate_source_excerpt);
  const candidateConfidence = numberText(card.candidate_confidence);
  const panelConfidence = numberText(card.panel_confidence);
  const panelDecision = str(card.panel_decision);
  const panelRationale = str(card.panel_rationale);
  const findingCount = Number(card.panel_finding_count) || 0;
  const riskCount = Number(card.panel_risk_count) || 0;
  const failedCount = Number(card.panel_failed_count) || 0;

  return (
    <div className="mt-3 rounded-xl border border-violet-100 bg-violet-50/60 px-3 py-3">
      <div className="flex flex-wrap items-center gap-1.5">
        {candidateId ? <Chip tone="violet">{candidateId}</Chip> : null}
        {state ? <Chip tone="gray">{state}</Chip> : null}
        {visibility ? <Chip tone="gray">{visibility}</Chip> : null}
        {candidateConfidence ? <Chip tone="orange">claim {candidateConfidence}</Chip> : null}
        {panelDecision ? <Chip tone="blue">panel {panelDecision}</Chip> : null}
        {panelConfidence ? <Chip tone="orange">panel {panelConfidence}</Chip> : null}
      </div>

      {object ? (
        <p className="mt-2 text-[13px] font-medium leading-relaxed text-ink">{shortText(object, 360)}</p>
      ) : (
        <p className="mt-2 text-[12.5px] leading-relaxed text-ink-3">
          Candidate text is not present in the workbench projection.
        </p>
      )}

      {(sourceTitle || sourceExcerpt || itemRef || extractedBy || extractionId) ? (
        <div className="mt-3 rounded-lg border border-violet-100 bg-white/70 px-3 py-2.5">
          <div className="section-label mb-1">Where this came from</div>
          {sourceTitle ? <p className="text-[12.5px] font-medium leading-relaxed text-ink">{sourceTitle}</p> : null}
          {sourceExcerpt ? (
            <p className="mt-1.5 text-[12px] leading-relaxed text-ink-3">{shortText(sourceExcerpt, 420)}</p>
          ) : null}
          <div className="mt-2 flex flex-wrap gap-1.5">
            {sourceKind ? <Chip tone="gray">{sourceKind}</Chip> : null}
            {extractionId ? <Chip tone="violet">{extractionId}</Chip> : null}
          </div>
          {itemRef ? <code className="mono mt-2 block break-all text-[10.5px] text-ink-3">{itemRef}</code> : null}
          {extractedBy ? <code className="mono mt-2 block break-all text-[10.5px] text-ink-3">{extractedBy}</code> : null}
        </div>
      ) : null}

      <div className="mt-3 grid gap-2 sm:grid-cols-2">
        {predicate ? (
          <div>
            <div className="section-label mb-1">Claim type</div>
            <code className="mono break-all text-[11px] text-ink-3">{predicate}</code>
          </div>
        ) : null}
        {entity ? (
          <div>
            <div className="section-label mb-1">Source entity</div>
            <code className="mono break-all text-[11px] text-ink-3">{entity}</code>
          </div>
        ) : null}
      </div>

      <div className="mt-3 flex flex-wrap gap-1.5 text-[11.5px] text-ink-3">
        <span>{findingCount} panel finding{findingCount === 1 ? "" : "s"}</span>
        <span>·</span>
        <span>{riskCount} caution{riskCount === 1 ? "" : "s"}</span>
        {failedCount ? (
          <>
            <span>·</span>
            <span>{failedCount} failed lens</span>
          </>
        ) : null}
      </div>

      {panelRationale ? (
        <p className="mt-2 text-[12px] leading-relaxed text-ink-3">{shortText(panelRationale, 260)}</p>
      ) : null}
      {sourceRef ? <code className="mono mt-2 block break-all text-[10.5px] text-ink-3">{sourceRef}</code> : null}
    </div>
  );
}

function MemoryDecisionForm({ card }: { card: Json }) {
  const candidateId = str(card.candidate_id);
  if (!candidateId || str(card.kind) !== "memory_candidate_adjudication") return null;

  return (
    <DecisionForm
      itemId={candidateId}
      formSpec={{
        area: "memory-adjudicate",
        defaultOption: "park",
        options: [
          {
            value: "park",
            label: "Keep parked",
            tone: "orange",
            description: "Keep as evidence. It will not enter ambient recall.",
            argv: ["memory", "adjudicate", candidateId, "--verdict", "park"],
          },
          {
            value: "no_promote",
            label: "Do not promote",
            tone: "gray",
            description: "Keep the candidate, but block promotion from this source.",
            argv: ["memory", "adjudicate", candidateId, "--verdict", "no_promote"],
          },
          {
            value: "reject",
            label: "Reject",
            tone: "red",
            description: "Mark it wrong or noisy so it is not reused.",
            argv: ["memory", "adjudicate", candidateId, "--verdict", "reject"],
          },
          {
            value: "promote",
            label: "Promote",
            tone: "green",
            description: "Accept as durable memory. Pick the evidence basis below.",
            argv: ["memory", "adjudicate", candidateId, "--verdict", "promote"],
          },
        ],
        fields: [
          {
            name: "basis",
            type: "select",
            label: "Promotion reason",
            required: true,
            defaultValue: "explicit_confirmation",
            showForOptions: ["promote"],
            options: [
              { value: "explicit_confirmation", label: "I confirm this" },
              { value: "curated_page", label: "Curated page" },
              { value: "decision_record", label: "Decision record" },
              { value: "runtime_proof", label: "Runtime proof" },
              { value: "trusted_deterministic_import", label: "Trusted import" },
            ],
          },
        ],
      }}
    />
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
      <MemoryCandidatePreview card={card} />
      <MemoryDecisionForm card={card} />

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
