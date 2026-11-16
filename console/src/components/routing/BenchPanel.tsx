
/**
 * Bench — honest doctrine placeholder. `compound bench` is file-based
 * (verified 2026-07-09: `bench --help` → `bench run --suite <file>` /
 * `bench compare --before <report> --after <report>`); it neither lists nor
 * projects "the latest report", and its output carries private ids meant to
 * live under the compound home, never the repo. So the console does NOT fabricate
 * a bench read — it names exactly how a report is produced and hands over the
 * copyable commands. When a cheap `bench report --json` projection lands, this
 * panel becomes a real table; until then it tells the truth.
 */
export function BenchPanel() {
  return (
    <section className="flex flex-col gap-2">
      <h2 className="text-md font-semibold text-ink">Bench</h2>
      <p className="max-w-prose text-sm leading-relaxed text-muted">
        No benchmark results are shown here yet: a benchmark writes its results to a file this console does not read.
        An agent runs one when a model change needs proof.
      </p>
    </section>
  );
}
