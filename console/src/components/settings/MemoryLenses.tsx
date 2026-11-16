import { str, type Json } from "@/lib/compound";

import { DatabaseIcon } from "@/components/kit";

import { Card, DoctrineNote, FileOwnedFoot, InspectRow, KV, SettingsSection } from "./primitives";

/**
 * Group 4 — Memory & lenses. Engine profile (read-only), the memory-policy tier
 * doctrine (ambient / contextual / forbidden), and the panel lens prompts —
 * which need a target-kind argument, so they get an honest inspect row instead
 * of a fabricated list.
 */
/** What each memory search setting does, in words. */
const PROFILE_WORDS: Record<string, string> = {
  breadth: "it searches Compound's own records first and your wider notes as supporting evidence",
  generic: "it uses the basic built-in search",
};

export function MemoryLenses({ engine, generatedAt }: { engine: Json | null; generatedAt?: string }) {
  const profile = str(engine?.profile) || "—";
  const configured = Boolean(engine?.configured);

  const tiers: { tier: string; tone: string; copy: string }[] = [
    { tier: "ambient", tone: "text-good", copy: "Only promoted beliefs surface ambiently — the system asserts them without being asked." },
    { tier: "contextual", tone: "text-muted", copy: "Contextual memory loads on demand through the class-tier valve — retrieved when the work calls for it." },
    { tier: "forbidden", tone: "text-crit", copy: "Forbidden/parked material never surfaces ambiently; it stays evidence until adjudicated and promoted." },
  ];

  return (
    <SettingsSection
      index={4}
      icon={<DatabaseIcon />}
      tone="memory"
      title="Memory & lenses"
      question="What does the system believe by default, and how are candidates judged?"
      source="compound memory engine-config --json"
      generatedAt={generatedAt}
    >
      <div className="grid gap-4 md:grid-cols-2">
        <Card title="Engine profile">
          <p className="text-sm leading-relaxed text-ink">
            {engine === null
              ? "The memory search settings could not be read."
              : configured
                ? `Memory search uses the "${profile}" setting${PROFILE_WORDS[profile] ? `: ${PROFILE_WORDS[profile]}` : ""}. It is chosen in its settings file; nothing in this console changes it.`
                : "No memory search setting is chosen, so the built-in default applies."}
          </p>
          <FileOwnedFoot
            cmd="compound memory engine-config"
            note="Engine profile is file-owned config (memory-engine.yaml). Read-only from the console."
          />
        </Card>

        <Card title="Memory policy tiers">
          <div className="flex flex-col gap-2.5">
            {tiers.map((t) => (
              <div key={t.tier} className="flex flex-col gap-0.5">
                <span className={`text-xs font-semibold uppercase tracking-wide ${t.tone}`}>{t.tier}</span>
                <span className="text-2xs leading-relaxed text-muted">{t.copy}</span>
              </div>
            ))}
          </div>
          <div className="mt-3 border-t border-hair pt-2.5">
            <DoctrineNote>Curated brain wins over the raw corpus on any durable claim; the corpus is evidence, not canon.</DoctrineNote>
          </div>
        </Card>
      </div>

      <InspectRow
        label="Panel lens prompts"
        cmd="compound panel lenses --target-kind <kind> --json"
        reason="The questions used to judge proposed memories differ for each kind of record, so there is no single list to show here yet."
      />
    </SettingsSection>
  );
}
