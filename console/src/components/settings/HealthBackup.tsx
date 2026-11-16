import { SelfHealingPanel } from "@/components/briefing/SelfHealingPanel";
import { num, type ProjectionRead } from "@/lib/compound";
import { selfHealing } from "@/lib/briefing";

import { HeartPulseIcon } from "@/components/kit";

import { Card, DoctrineNote, ForAgents, KV, SettingsSection } from "./primitives";

/**
 * Group 7 — Health & backup. Reuses the briefing SelfHealingPanel (home-doctor
 * hygiene with honest repair copy-commands — imported, not duplicated), plus
 * the home-doctor facts and the documented proof-check / backup / restore /
 * archive-history commands. These are operator terminal ops, not console
 * mutations, so they are honest copy-commands.
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
      <SelfHealingPanel health={health} />
    </SettingsSection>
  );
}
