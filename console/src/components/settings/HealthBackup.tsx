import { CopyCmd } from "@/components/CopyCmd";
import { SelfHealingPanel } from "@/components/briefing/SelfHealingPanel";
import { num, type Json } from "@/lib/compound";
import { selfHealing } from "@/lib/briefing";

import { Card, DoctrineNote, KV, SettingsSection } from "./primitives";

/**
 * Group 7 — Health & backup. Reuses the briefing SelfHealingPanel (home-doctor
 * hygiene with honest repair copy-commands — imported, not duplicated), plus
 * the home-doctor facts and the documented proof-check / backup / restore /
 * archive-history commands. These are operator terminal ops, not console
 * mutations, so they are honest copy-commands.
 */
const DOCUMENTED_COMMANDS: { label: string; cmd: string; note: string }[] = [
  { label: "Proof-check", cmd: "compound proof-check --json", note: "Deterministic local gate bundle: tests, vet, build, gofmt, diff, doctors, redact." },
  { label: "Backup", cmd: "compound home backup", note: "Snapshot the runtime home to a tar.gz under exports/." },
  { label: "Restore", cmd: "compound home restore <archive.tar.gz>", note: "Restore the runtime home from a snapshot archive." },
  { label: "Archive history", cmd: "compound home archive-history --kind work", note: "Move terminal work/leases out of hot state — the hygiene repair." },
  { label: "Restore history", cmd: "compound home restore-history", note: "Bring archived history back into hot state." },
];

export function HealthBackup({ doctor, generatedAt }: { doctor: Json | null; generatedAt?: string }) {
  const health = selfHealing(doctor);
  const manifestOk = doctor?.manifest_ok !== false;
  const dirsOk = num(doctor?.dirs_ok);
  const dirsTotal = num(doctor?.dirs_total);
  const insideRepo = Boolean(doctor?.inside_git_repo);
  const stateInRepo = Boolean(doctor?.generated_state_in_repo_source);

  return (
    <SettingsSection
      index={7}
      title="Health & backup"
      question="Is the runtime home intact, and how do I prove, snapshot, or restore it?"
      source="compound home doctor --json"
      generatedAt={generatedAt}
    >
      <div className="grid gap-4 md:grid-cols-2">
        <Card title="Home doctor" seamTone={health.ok ? "good" : "seam"}>
          <KV k="manifest_ok" v={manifestOk ? "true" : "false"} />
          <KV k="dirs" v={dirsTotal ? `${dirsOk}/${dirsTotal}` : "—"} />
          <KV k="inside_git_repo" v={insideRepo ? "true" : "false"} />
          <KV k="generated_state_in_repo" v={stateInRepo ? "true" : "false"} />
          <DoctrineNote>
            <span className="mt-2 block">
              Generated state belongs in the runtime home, never the repo. A true here would be a doctrine breach the
              doctor flags for repair.
            </span>
          </DoctrineNote>
        </Card>

        <Card title="Prove · snapshot · restore">
          <div className="flex flex-col gap-2.5">
            {DOCUMENTED_COMMANDS.map((entry) => (
              <div key={entry.cmd} className="flex flex-col gap-1">
                <span className="text-xs font-semibold text-ink">{entry.label}</span>
                <span className="text-2xs leading-relaxed text-muted">{entry.note}</span>
                <CopyCmd cmd={entry.cmd} />
              </div>
            ))}
          </div>
        </Card>
      </div>

      <SelfHealingPanel health={health} />
    </SettingsSection>
  );
}
