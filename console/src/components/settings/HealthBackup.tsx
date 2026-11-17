import { num, type ProjectionRead } from "@/lib/compound";
import { selfHealing, type SelfHealing } from "@/lib/briefing";

import { HeartPulseIcon } from "@/components/kit";

import { Card, DoctrineNote, ForAgents, KV, SettingsSection } from "./primitives";

/**
 * Group 7 — Health & backup. The home check's findings (lib/briefing.ts
 * `selfHealing`), each as one plain sentence on what it means and who
 * handles it; the repair commands sit behind a closed "For agents"
 * disclosure, never as his action. Plus the home-doctor facts and the
 * documented proof-check / backup / restore / archive-history commands for
 * agents.
 */
const DOCUMENTED_COMMANDS: { label: string; cmd: string; note: string }[] = [
  { label: "Source checks", cmd: "compound proof-check --dev --base main", note: "For contributors in a source checkout. Non-certifying checks; release acceptance uses the governed full proof ceremony." },
  { label: "Backup", cmd: "compound home backup", note: "Snapshot runtime home to tar.gz under exports/. Quiesce writers first for a consistent recovery set." },
  { label: "Verify backup", cmd: "compound home verify <archive.tar.gz>", note: "Check the archive before recovery. Creating a backup alone does not prove restoration." },
  { label: "Restore to a new home", cmd: "compound home restore <archive.tar.gz> --target <new-absent-home-path>", note: "Replace placeholders with your archive and a new absolute directory. Does not overwrite or activate your current home." },
  { label: "Preview history archive", cmd: "compound home archive-history --kind work --dry-run", note: "Inspect eligible terminal history first. Removing --dry-run changes the home." },
  { label: "Preview history restore", cmd: "compound home restore-history --id <archive-id> --dry-run", note: "Use the exact archive ID from the archive-history result. Inspect the plan before restoring into hot state." },
];

export function HealthBackup({ doctor }: { doctor: ProjectionRead }) {
  const health = selfHealing(doctor);
  const value = doctor.status === "unreachable" ? null : doctor.value;
  const bool = (key: string) => typeof value?.[key] === "boolean" ? String(value[key]) : "unknown";
  const dirsOk = num(value?.dirs_ok);
  const dirsTotal = num(value?.dirs_total);
  return (
    <SettingsSection
      index={7}
      icon={<HeartPulseIcon />}
      tone="good"
      title="Health and recovery"
      question="Is Compound's home intact, and is anything waiting for a repair?"
      source="compound home doctor --json"
      generatedAt={health.observation === "live" ? doctor.generatedAt ?? undefined : undefined}
    >
      <Card title={doctor.status === "stale" ? "Compound's home · last known" : "Compound's home"} seamTone={value === null ? undefined : health.ok ? "good" : "seam"}>
        <p className="text-sm leading-relaxed text-ink">
          {value === null
            ? "The health check could not be read."
            : typeof value?.manifest_ok !== "boolean"
              ? "The health check came back incomplete, so this page does not say the home is intact."
              : health.ok
                ? `Compound's home is intact${dirsTotal > 0 ? `: all ${dirsTotal.toLocaleString("en-US")} folders are in place` : ""}.`
                : "Compound's home needs a repair; the list below says what. An agent carries out repairs; nothing in this console does."}
        </p>
        <p className="mt-1 text-2xs leading-relaxed text-faint">Backups and restores are run by an agent when you ask; this console starts none.</p>
        <ForAgents cmds={DOCUMENTED_COMMANDS.map((entry) => entry.cmd)}>
          <KV k="manifest_ok" v={bool("manifest_ok")} />
          <KV k="dirs" v={Number.isInteger(value?.dirs_ok) && dirsTotal > 0 ? `${dirsOk}/${dirsTotal}` : "unknown"} />
          <KV k="inside_git_repo" v={bool("inside_git_repo")} />
          <KV k="generated_state_in_repo" v={bool("generated_state_in_repo_source")} />
        </ForAgents>
      </Card>
      <HealthFindings health={health} />
    </SettingsSection>
  );
}

const plural = (n: number, one: string, many: string) => `${n.toLocaleString("en-US")} ${n === 1 ? one : many}`;
const ASK = "An agent does it when you ask in chat; this page knows of no scheduled job that does.";

/**
 * Each finding of the home check in plain words: what it means for him, and
 * who handles it. Keyed by the finding's key (lib/briefing.ts `selfHealing`);
 * a finding with no words here keeps its own label.
 */
const FINDING_WORDS: Readonly<Record<string, (n: number) => { what: string; who: string }>> = {
  "hot-work": (n) => ({
    what: `${plural(n, "finished work item still sits", "finished work items still sit")} with the open work. Nothing is lost, but every read of open work is slower for it.`,
    who: `Moving them to the archive is a cleanup. ${ASK}`,
  }),
  "stale-leases": (n) => ({
    what: `${plural(n, "record", "records")} of an agent holding work ${n === 1 ? "is" : "are"} left over from work that has finished. They block nothing.`,
    who: `Archiving them is a cleanup. ${ASK}`,
  }),
  manifest: () => ({
    what: "Compound's home folder no longer matches its own list of what belongs there.",
    who: `An agent runs the home check and repairs it. ${ASK}`,
  }),
  dirs: (n) => ({
    what: `${plural(n, "folder", "folders")} Compound needs ${n === 1 ? "is" : "are"} missing from its home.`,
    who: `An agent puts ${n === 1 ? "it" : "them"} back. ${ASK}`,
  }),
  "repo-state": () => ({
    what: "Files Compound makes for itself sit inside a code folder, where they do not belong.",
    who: `An agent moves them out. ${ASK}`,
  }),
  doctor: () => ({
    what: "The home check reported a problem this page cannot name.",
    who: "An agent reads the full report when you ask in chat.",
  }),
};

/** The home check's findings: one plain sentence each and who handles it; commands only behind "For agents". */
function HealthFindings({ health }: { health: SelfHealing }) {
  const unknown = health.observation === "unreachable" || health.observation === "invalid";
  const stale = health.observation === "stale";
  if (unknown) {
    return (
      <Card title="What needs a repair" seamTone="seam">
        <p className="text-sm leading-relaxed text-ink">
          Unknown: the home check could not be read{health.observation === "invalid" ? " in full" : ""}, so this page cannot say what needs a repair. It reads the check again by itself.
        </p>
        <ForAgents cmds={["compound home doctor --json"]} />
      </Card>
    );
  }
  if (health.ok || (stale && health.observedOK)) {
    return (
      <Card title="What needs a repair" seamTone="good">
        <p className="text-sm leading-relaxed text-ink">
          {stale ? "Nothing, at the last known check; this page reads the check again by itself." : "Nothing. Clean home: the home check found nothing to repair."}
        </p>
      </Card>
    );
  }
  return (
    <Card title={stale ? "What needs a repair · last known" : "What needs a repair"} seamTone="seam">
      {stale ? <p className="mb-3 text-xs text-seam">These are the findings of the last check that came back; this page reads the check again by itself.</p> : null}
      <ul className="flex flex-col gap-3">
        {health.flags.map((flag) => {
          const words = FINDING_WORDS[flag.key]?.(flag.count);
          return (
            <li key={flag.key} className="border-l-[3px] pl-3" style={{ borderLeftColor: flag.tone === "crit" ? "var(--crit-fg)" : "var(--seam-fg)" }}>
              <p className="text-sm leading-relaxed text-ink">{words?.what ?? `${flag.count.toLocaleString("en-US")} · ${flag.label}.`}</p>
              <p className="text-xs leading-relaxed text-muted">{words?.who ?? "An agent looks into it when you ask in chat."}</p>
            </li>
          );
        })}
      </ul>
      <ForAgents cmds={[...new Set(health.flags.map((flag) => flag.command))]} />
    </Card>
  );
}
