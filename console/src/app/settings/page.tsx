import Link from "next/link";
import type { ReactNode } from "react";
import { PageHeader, plainAge, ReadChip, ReadNotice, readsPending } from "@/components/kit";
import { PendingRefresh } from "@/components/PendingRefresh";
import { AutonomySafety } from "@/components/settings/AutonomySafety";
import { ConsoleEnv } from "@/components/settings/ConsoleEnv";
import { HarnessesHooks } from "@/components/settings/HarnessesHooks";
import { HealthBackup } from "@/components/settings/HealthBackup";
import { MemoryLenses } from "@/components/settings/MemoryLenses";
import { PolicyGroup } from "@/components/settings/PolicyGroup";
import { RoutingModels } from "@/components/settings/RoutingModels";
import { readAttention } from "@/lib/attention";
import { HEAVY_PROJECTION_TIMEOUT_MS, privateFallbacksEnabled, readProjection, str, type LiveRead, type ProjectionRead } from "@/lib/compound";
import { consoleMutationsEnabled } from "@/lib/console-mutations";
import { computeConsoleMutationReadiness } from "@/lib/instance-pin";

export const dynamic = "force-dynamic";

function asProjection(read: LiveRead): ProjectionRead {
  return { ...read, status: read.status === "loading" ? "unreachable" : read.status };
}

/** What each read is, in words, for the labelled freshness chips. */
const READ_LABEL: Record<string, string> = {
  "policy autonomy-halt": "Stop switch",
  "policy operator-authority": "Autonomy level",
  "policy standing-grants-authority": "Permissions",
  "policy editable-surfaces": "Setup changes",
  "policy placement-authority": "Where work runs",
  "policy operator-approval-frontier-authority": "What needs your approval",
  "policy operator-prompts-authority": "Questions for you",
  "policy release-goal-holds-authority": "Release holds",
  "decisions authority": "Who decides what",
  "route evidence-authority": "Model usage",
  "memory engine-config": "Memory search",
  "contract hooks-authority": "Agent connections",
  "home doctor": "Health check",
};

function readLabel(read: LiveRead): string {
  const command = read.source.replace(/^compound /, "").replace(/ --json$/, "");
  return READ_LABEL[command] ?? command;
}

/** The section's state in words: how old its data is, still reading, or what could not be read. */
function sectionStatus(reads: LiveRead[]): string {
  if (!reads.length) return "";
  const failed = reads.filter((read) => read.value === null && read.status === "unreachable");
  if (failed.length) return `Could not read: ${failed.map(readLabel).join(", ")}`;
  if (reads.some((read) => read.value === null)) return "Reading…";
  const ages = reads.map((read) => read.ageMs).filter((age): age is number => age !== null);
  return ages.length ? `Read ${plainAge(Math.max(...ages))}` : "Read";
}

/**
 * Never render an unknown setting as zero, a disabled permission, or a
 * healthy system. A `partial` section renders what loaded and marks only the
 * missing part; its cards say "not read" for what is missing.
 */
function SettingsGroup({ title, description, reads, children, selected, partial = false }: {
  title: string; description: string; reads: LiveRead[]; children: ReactNode; selected: string; partial?: boolean;
}) {
  const missing = reads.filter((read) => read.value === null);
  const key = title.toLowerCase().replace(/[^a-z]+/g, "-");
  const open = selected === key;
  const status = sectionStatus(reads);
  return (
    <section id={`settings-${key}`} className="scroll-mt-20 rounded-xl border border-hair bg-panel">
      <Link href={open ? "/settings" : `/settings?section=${key}#settings-${key}`} scroll={false} aria-expanded={open} aria-controls={`settings-body-${key}`} className="flex flex-wrap items-center justify-between gap-3 rounded-xl px-5 py-4 hover:bg-soft">
        <div><h2 className="text-base font-semibold text-ink">{title}</h2><p className="mt-1 text-sm text-muted">{description}</p></div>
        <span className="flex items-center gap-3"><span className="text-xs text-muted">{status}</span><span aria-hidden className={open ? "rotate-90" : ""}>›</span></span>
      </Link>
      {open ? <div id={`settings-body-${key}`} className="grid gap-4 border-t border-hair p-5">
        {reads.length ? <div className="flex flex-wrap gap-x-4 gap-y-2">{reads.map((read) => <span key={read.source} className="inline-flex items-center gap-1.5 text-2xs text-muted">{readLabel(read)} <ReadChip read={read} /></span>)}</div> : null}
        {missing.map((read) => <ReadNotice key={read.source} read={read} label={readLabel(read).toLowerCase()} />)}
        {!missing.length || partial ? children : null}
      </div> : null}
    </section>
  );
}

export default async function Settings({ searchParams }: { searchParams?: Promise<{ section?: string }> }) {
  const selected = (await searchParams)?.section ?? "";
  const commands = [
    ["policy", "autonomy-halt", "--json"], ["policy", "operator-authority", "--json"],
    ["policy", "standing-grants-authority", "--json"], ["policy", "editable-surfaces", "--json"],
    ["policy", "placement-authority", "--json"], ["policy", "operator-approval-frontier-authority", "--json"],
    ["policy", "operator-prompts-authority", "--json"], ["policy", "release-goal-holds-authority", "--json"],
    ["decisions", "authority", "--json"], ["route", "evidence-authority", "--json"],
    ["memory", "engine-config", "--json"], ["contract", "hooks-authority"], ["home", "doctor", "--json"],
  ];
  const [reads, attentionProjection] = await Promise.all([
    Promise.all(commands.map((args) => readProjection(args, { timeoutMs: HEAVY_PROJECTION_TIMEOUT_MS }))),
    readAttention(),
  ]);
  const [halt, authority, grants, editable, placement, frontier, prompts, holds, decisions, routeEvidence, engine, hooks, doctor] = reads;
  const { read: attentionRead, report: attention } = attentionProjection;
  const mutationReadiness = computeConsoleMutationReadiness(consoleMutationsEnabled());
  return (
    <div className="flex flex-col gap-5">
      <PendingRefresh pending={readsPending(...reads, attentionRead)} stamp={Date.now()} />
      <PageHeader name="Settings" lead="See what Compound may do, how it connects, and whether it is healthy." />
      <SettingsGroup selected={selected} partial title="Safety & permissions" description="Whether agents are stopped, how far they go on their own, and what they may do without asking." reads={reads.slice(0, 4)}>
        <AutonomySafety halt={halt.value} authority={authority.value} grants={grants.value} editable={editable.value} generatedAt={grants.generatedAt ?? undefined} nowMs={Date.now()} />
      </SettingsGroup>
      <SettingsGroup selected={selected} title="Operating rules" description="Approval rules, release holds and decision ownership." reads={reads.slice(4, 9)}>
        <PolicyGroup placement={placement.value} frontier={frontier.value} prompts={prompts.value} holds={holds.value} decisions={decisions.value} generatedAt={prompts.generatedAt ?? undefined} />
      </SettingsGroup>
      <SettingsGroup selected={selected} title="Models & routing" description="Providers, usage and which model handles the work." reads={[routeEvidence]}>
        <RoutingModels routeEvidence={routeEvidence.value} promotionCount={null} generatedAt={routeEvidence.generatedAt ?? undefined} />
      </SettingsGroup>
      <SettingsGroup selected={selected} title="Memory" description="How stored evidence is searched and recalled." reads={[engine]}>
        <MemoryLenses engine={engine.value} />
      </SettingsGroup>
      <SettingsGroup selected={selected} title="Agent connections" description="Installed hooks and the protections they enforce." reads={[hooks]}>
        <HarnessesHooks hooks={asProjection(hooks)} generatedAt={hooks.generatedAt ?? undefined} />
      </SettingsGroup>
      <SettingsGroup selected={selected} title="This console" description="Connection status and what this browser can do." reads={[]}>
        <ConsoleEnv mutationsEnabled={mutationReadiness.ready} mutationFlagEnabled={mutationReadiness.flagEnabled} instancePinned={mutationReadiness.instancePinned} standardClientAuthorized={mutationReadiness.standardClientAuthorized} privateFallbacks={privateFallbacksEnabled()} attention={attention} attentionRead={attentionRead} generatedAt={str(attention?.generated_at) || undefined} />
      </SettingsGroup>
      <SettingsGroup selected={selected} title="Health & recovery" description="Storage checks, backups and repair commands." reads={[doctor]}>
        <HealthBackup doctor={asProjection(doctor)} />
      </SettingsGroup>
    </div>
  );
}
