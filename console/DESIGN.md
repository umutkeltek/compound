# Compound Console — Design System

The visual contract for every console screen and component. Implementation PRs are reviewed
against this file; deviations need a written reason here first. IA companion: the redesign plan
(operator-held); runtime grammar: `docs/architecture/runtime.md`.

## 0. The organizing law

Three orthogonal axes, each with its own hue family and its own placement. They never swap.

| Axis | Question | Hue family | Where it appears |
|---|---|---|---|
| **Domain** | which subsystem? | cool: blue, violet, cyan, indigo, pink | glyph tile, 3px left seam, dot, label text — **accent only, never a fill** |
| **State** | where in its lifecycle? | neutral + blue-live + the warm trio | the **pill**, right side of a card |
| **Severity** | how alarming? | warm: green, amber, red | health tiles, meters, banners |

Amber-as-fill is reserved for exactly one thing: the **admission seam** — a card that needs the
operator gets a 3px amber left seam + a left-to-right amber wash. Scanning left edges down a page
answers "what needs me."

A "running memory adjudication" therefore reads: violet seam (memory) + blue `running` pill
(state) + green freshness dot (live data). Three signals, zero collision.

## 1. Design tokens — Tailwind v4 `@theme`

Colors flip at runtime via `[data-theme]` on `:root`, so they live as plain custom properties
registered into utility generation with `@theme inline`. Static scales (type, radius, motion,
layout) live directly in `@theme`. Spacing rides Tailwind's built-in 4px scale — arbitrary px
values in class names are banned (§7).

```css
@import "tailwindcss";

/* ============================================================
   STATIC SCALES  (theme-independent)
   ============================================================ */
@theme {
  /* — type: 13.5px base, explicit line-heights, tabular for numerics — */
  --text-2xs: 10.5px;   --text-2xs--line-height: 1.4;    /* inline mono ids, boundary tags */
  --text-xs:  11.5px;   --text-xs--line-height: 1.45;    /* meta, section labels, pills */
  --text-sm:  12.5px;   --text-sm--line-height: 1.5;     /* secondary body */
  --text-base:13.5px;   --text-base--line-height: 1.55;  /* DEFAULT body */
  --text-md:  14.5px;   --text-md--line-height: 1.5;     /* card titles emphasis */
  --text-lg:  17px;     --text-lg--line-height: 1.35;    /* drawer title */
  --text-xl:  22px;     --text-xl--line-height: 1.2;
  --text-hero:24px;     --text-hero--line-height: 1.24;  /* primary operator question */
  --text-2xl: 30px;     --text-2xl--line-height: 1.1;    /* KPI value */

  --font-sans: -apple-system, BlinkMacSystemFont, "SF Pro Text", "Segoe UI", Roboto, system-ui, sans-serif;
  --font-mono: "SF Mono", ui-monospace, SFMono-Regular, "JetBrains Mono", Menlo, monospace;

  /* — radii — */
  --radius-xs: 6px;     --radius-sm: 9px;    --radius-md: 12px;
  --radius-lg: 14px;    /* the card radius */
  --radius-xl: 18px;    --radius-pill: 100px;

  /* — semantic layout sizes (the only "spacing" tokens) — */
  --rail-w: 52px;   --nav-w: 218px;   --drawer-w: 460px;   --composer-w: 560px;   --read-max: 720px;

  /* — motion — */
  --dur-fast: 120ms;   --dur-base: 180ms;   --dur-slow: 240ms;   --dur-slower: 400ms;
  --ease-out: cubic-bezier(.2,.7,.3,1);
  --ease-drawer: cubic-bezier(.3,.8,.3,1);
  --ease-standard: cubic-bezier(.4,0,.2,1);

  /* — register runtime color vars as utilities (text-work, bg-seam-soft, border-hair …) — */
  --color-page: var(--page);       --color-panel: var(--panel);
  --color-soft: var(--soft);       --color-soft-2: var(--soft-2);
  --color-ink: var(--ink);         --color-strong: var(--strong);
  --color-muted: var(--muted);     --color-faint: var(--faint);
  --color-hair: var(--hair);       --color-hair-2: var(--hair-2);

  --color-work: var(--work-fg);           --color-work-soft: var(--work-soft);       --color-work-line: var(--work-line);
  --color-memory: var(--memory-fg);       --color-memory-soft: var(--memory-soft);   --color-memory-line: var(--memory-line);
  --color-capability: var(--cap-fg);      --color-capability-soft: var(--cap-soft);  --color-capability-line: var(--cap-line);
  --color-route: var(--route-fg);         --color-route-soft: var(--route-soft);     --color-route-line: var(--route-line);
  --color-product: var(--product-fg);     --color-product-soft: var(--product-soft); --color-product-line: var(--product-line);

  --color-good: var(--good-fg);    --color-good-soft: var(--good-soft);   --color-good-line: var(--good-line);
  --color-seam: var(--seam-fg);    --color-seam-soft: var(--seam-soft);   --color-seam-line: var(--seam-line);
  --color-crit: var(--crit-fg);    --color-crit-soft: var(--crit-soft);   --color-crit-line: var(--crit-line);
  --color-live: var(--work-fg);    /* "active" == work blue, signalled by pulse, not a new hue */
}

/* ============================================================
   LIGHT  (primary)
   ============================================================ */
:root, :root[data-theme="light"] {
  color-scheme: light;
  --page:#F4F5F7; --panel:#FFFFFF; --soft:#F8F9FB; --soft-2:#F1F3F6;
  --ink:#101828; --strong:#0A101E; --muted:#51606F; --faint:#98A2B3;
  --hair:#E8EAEF; --hair-2:#EEF0F4;

  /* domain (cool) */
  --work-fg:#2563EB;   --work-soft:#EAF1FE;   --work-line:#C7DBFB;
  --memory-fg:#7C3AED; --memory-soft:#F1ECFD; --memory-line:#DDCFF8;
  --cap-fg:#0E93A6;    --cap-soft:#E1F4F7;    --cap-line:#B3E2EA;
  --route-fg:#4F46E5;  --route-soft:#ECEBFE;  --route-line:#CBC9F7;
  --product-fg:#DB2777;--product-soft:#FCE9F2;--product-line:#F6C2DA;

  /* severity / state-warm (temperature) */
  --good-fg:#059669;   --good-soft:#E4F6EF;   --good-line:#B4E6D3;
  --seam-fg:#B26F09;   --seam-soft:#FCF1DC;   --seam-line:#F1D89B;   /* the attention color */
  --crit-fg:#DC2626;   --crit-soft:#FCE9E7;   --crit-line:#F5C4BE;

  --shadow-sm:0 1px 2px rgba(16,26,38,.05);
  --shadow:0 1px 2px rgba(16,26,38,.04),0 4px 16px -6px rgba(16,26,38,.10);
  --shadow-lg:0 8px 40px -8px rgba(16,26,38,.28);
  --focus:var(--work-fg);
}

/* ============================================================
   DARK  (first-class — hues RE-PICKED for the dark substrate, not inverted)
   ============================================================ */
:root[data-theme="dark"] {
  color-scheme: dark;
  --page:#080B10; --panel:#12171F; --soft:#161C25; --soft-2:#1A212B;
  --ink:#E4E9F0; --strong:#F4F7FA; --muted:#93A0AF; --faint:#7D8996;
  --hair:#242C37; --hair-2:#1F2630;

  --work-fg:#67ADFF;   --work-soft:#102A42;   --work-line:#28527A;
  --memory-fg:#B49AFF; --memory-soft:#221B42; --memory-line:#443873;
  --cap-fg:#50D4E6;    --cap-soft:#0F3039;    --cap-line:#23606B;
  --route-fg:#9799FF;  --route-soft:#1D1F43;  --route-line:#3C3F78;
  --product-fg:#FF83C2;--product-soft:#361228;--product-line:#6A2850;

  --good-fg:#4BD092;   --good-soft:#112F23;   --good-line:#275840;
  --seam-fg:#F0AE48;   --seam-soft:#302510;   --seam-line:#654A18;
  --crit-fg:#FF7A68;   --crit-soft:#331714;   --crit-line:#633029;

  --shadow-sm:0 1px 2px rgba(0,0,0,.3);
  --shadow:0 1px 2px rgba(0,0,0,.35),0 8px 24px -12px rgba(0,0,0,.6);
  --shadow-lg:0 10px 50px -8px rgba(0,0,0,.7);
  --focus:var(--work-fg);
}

body { background:var(--color-page); color:var(--color-ink);
       font-family:var(--font-sans); font-size:var(--text-base);
       -webkit-font-smoothing:antialiased; }

/* numbers always align */
.tnum, .mono, [data-num] { font-variant-numeric: tabular-nums; }
```

**Type/mono rules.** Base 13.5px; operator questions use the 24px hero step rather than scaling
ordinary card text into a second headline system. Mono (tabular) is mandatory for: every machine id (`work_…`,
`ctx_…`, `rec_…`, `tr_…`, lease/node ids), all timestamps, costs/`$`, counts inside pills and
badges, argv previews, lease durations. Sans is for prose. Weights: 400 body · 500 secondary
labels · 600 titles/nav-active · 700 uppercase section labels · 800 the KPI value only.

## 2. Status pill — the one grammar (~15 states → 7 tones)

`inline-flex; font-size:var(--text-xs); font-weight:650; padding:2px 9px;
border-radius:var(--radius-pill); border:1px solid; tabular-nums`.
Tone sets `background:*-soft; border-color:*-line; color:*-fg`.

| Tone | States covered | Treatment |
|---|---|---|
| `draft` | proposed, suggested, candidate | slate, **hollow outline** — "nothing runs yet" |
| `queued` | admitted, ready, claimed, watching, parked | slate filled — "in line" |
| `live` | running, in_progress, verifying | work-blue + **leading pulse dot** — the only animated pill |
| `seam` | held, needs_review, blocked, hold, needs-you | **amber** — your gate |
| `done` | completed, promoted, allow, filed | good-green |
| `failed` | failed, deny, rejected | crit-red |
| `dropped` | cancelled, demoted, no_promote | muted, no border emphasis |

This replaces `STATE_TONE`/`MOVE_TONE` maps where `claimed`→violet stole the memory hue. Under
this grammar `claimed` is `queued` (neutral); domain lives on the seam only.

## 3. Component anatomy

Shared card grammar: **glyph tile (domain) · title (sans) · state pill (right) · mono id ·
plain-language body · consequences · one primary act.** Padding 12–14px, `--radius-lg`, hairline
border, `--shadow-sm` at rest, `--shadow` + 1px lift on hover (`--dur-fast`).

The code contract for that grammar is `representation.ObjectFace`: human kind + label, the shared
state vocabulary, why it matters now, owner, freshness/confidence, and exactly one primary action
or an unavailable reason + unblock path. Machine IDs, CLI, and evidence remain secondary. Decide
is the reference consumer: its focus pane exposes **Summary · Action · History · Evidence**, and
its queue groups faces by the server-owned state into **Act now** and **Needs context**. Work,
Briefing, Memory, Agents, Capabilities, and Sources extend this face before inventing a bespoke
object card; their domain projection still owns truth and governed acts still cross the existing
action seam.

- **Decision card (triage hero):** domain tile + area label + plain-language headline (13px/600)
  with state pill top-right → body (12.5px muted) → consequences block on `--soft`
  (`if you act →` / `if you wait →`, act line in good-green) → act row: ONE primary button +
  "Details" + mono id (never truncated without `title`) → optional boundary tags + evidence
  (`<details>`, mono refs). Seam = domain hue; on the operator's gate, seam = amber.
- **Work card:** left seam recolors to STATE here (running=blue, needs-you=amber, failed=red) —
  the one place seam encodes state, because a board is scanned by state. Mono `work_id` top,
  title, clamped objective, meta row (owner, age, move), optional next-action hint, "Open".
- **Queue group / band card:** band header (Needs you / Watching / Filed) + count pill (amber
  only for needs-you) + collapse. Needs-you pinned top, severity-desc then age. Body = decision
  cards.
- **KPI stat:** uppercase label (11px/700, domain-tinted with leading dot for World Δ=work,
  Mind Δ=memory) → value (26px/800, tabular, tracking-tight) → sub (12px muted) → optional 60px
  SVG sparkline top-right at 50% opacity.
- **Trace drawer:** Radix Dialog, `width:min(var(--drawer-w),94vw)`, slide `--dur-slow`
  `--ease-drawer`, scrim fade + 2px blur. Header: title 16px/750 + mono meta. Body label
  "Envelope trace · intent → proof", vertical spine: 2px rail, 14px nodes (done=green fill,
  run=blue fill + beat, pending=hollow), each step = name + phase pill + description + mono meta.
  Footer: contextual governed acts — one primary, rest hairline.
- **Freshness chip:** dot + mono label, `title`=source command. The label is always the plain age of
  the copy (`updated 3 min ago`), plus `updating…` while a refresh runs. Green dot by default; amber
  only when the copy is older than 10 minutes or the last refresh failed. Unknown: faint. Every card
  showing projected data carries one.
- **Read states (never wait, never pretend):** pages read through the non-blocking `readProjection`
  (`lib/compound.ts`). A value with no copy yet renders as *loading* ("Reading … for the first time.
  Last time it took about 2 minutes."), a failed read with no copy as *failed*, and a copy whose
  refresh failed shows "Could not read …. Last good copy is from 10:42." above the last good value
  (`kit/ReadState.tsx`). A read that has not landed is never rendered as 0, empty or a doctrine
  empty state. While anything on a page is loading or updating, the page refreshes itself every
  5 seconds (`PendingRefresh`); the read queue, not the timer, decides when compound runs.
- **Empty state:** dashed hairline, `--page` bg, a plain sentence that says what is empty — never
  "No data" and never a metaphor ("Nothing is on hold.", "Nothing needs you today."; see §11.2), and
  only for a read that landed and is genuinely empty. Optional single CTA.
- **Nav item:** icon (15px, stroke 1.7, outline, currentColor) + label (13px) + right count pill
  (mono, tabular). Active: `--soft-2` bg + work-blue icon. **Only needs-you badges are amber; all
  other counts quiet slate.** Disabled/soon at 45% opacity.

## 4. Density & layout doctrine

- Shell: `--rail-w` icon rail + `--nav-w` grouped nav + fluid main; sticky topbar (breadcrumb,
  live chip, kill-switch chip, theme toggle).
- **moves → board · judge → cards · compare → table.** Boards for stage-flow (pipeline),
  card lists for per-item judgment (Decide, Memory), tables for homogeneous comparison
  (executors, leases, cost lanes).
- Tight within a card (12–14px), generous between groups (16–24px). Prose capped at `--read-max`.
- `:focus-visible` ring `2px solid var(--focus)` offset 2px on every interactive element.
  Cards `tabindex=0`, Enter/Space opens trace. Queue keys: `j/k` move, `a` act, `e` evidence,
  `x` exclude member, `/` filter, Esc closes overlays.

## 5. Motion

Purposeful only; tokens from §1.

- Live pulses: `ping` 2s (green data-live dot) · `beat` 1.4s (current trace node) · runbar 2.4s
  (executing work). Only on genuinely live data.
- **Signature interaction — optimistic act:** click → pill shifts to pending shimmer + card lift
  (`--dur-fast`); on confirm → settle to new tone with one soft ring-flash; on error → 1-cycle
  2px shake + inline error + revert.
- Drawer: translateX `--dur-slow` `--ease-drawer`; scrim `--dur-base`. Palette: scale(.98)→1 +
  fade `--dur-fast`. Streaming-in generative blocks: fade+slide `--dur-slower`, no bounce.
- `prefers-reduced-motion: reduce`: all keyframes/transitions off; pulses become static rings;
  optimistic swap is instant.

## 6. Dark/light

Light primary, dark first-class (hues re-picked, not inverted). Three-state toggle
(light/auto/dark), persisted, `color-scheme` set so native controls follow.

## 7. Anti-generic checklist (enforced in review)

1. No default Tailwind palette colors in markup (`blue-500`, `slate-100`, raw hex). Semantic
   tokens only (`text-work`, `bg-seam-soft`, `border-hair`).
2. Domain hue is accent-only — never a button fill, never a full-card fill. The only fills:
   neutral surfaces, the amber seam-wash, state pills, the one brand glyph tile.
3. `tabular-nums` on every number that aligns or changes.
4. Mono for every machine identifier; never truncated without a `title` carrying the full value.
5. No arbitrary px in class names (`text-[13.5px]`, `px-[19px]`) — type/radius scale or the 4px
   spacing scale.
6. One button system: exactly one primary per card (solid work or ink), secondary = hairline
   neutral, danger = crit outline.
7. Warm = temperature, cool = domain — never swapped. A domain never turns green; a "done" never
   turns violet.
8. Every card declares provenance (FreshnessChip: source + generated_at).
9. Empty states are doctrine, not "No data."
10. One icon grammar — 15–17px, stroke 1.7, `fill:none`, currentColor. No mixed icon sets.

## 8. Libraries

- **Radix primitives, behavior-only** for Dialog (trace drawer, details, ⌘K), Popover/DropdownMenu,
  Tabs, Tooltip — correct focus/keyboard/aria we'd hand-roll wrong. Styled with our tokens; this
  is not shadcn adoption.
- **Charts: hand-built SVG** (`<Sparkline>`, `<Meter>`, `<Bars>`) matching tokens. No chart lib
  for v1. `@xyflow/react` stays for topology/memory/lineage graphs.

## 9. Ambient depth & the data-viz vocabulary (v2 — operator-ratified direction, 2026-07-09)

The operator supplied reference material and the ruling: *"people should want to look at this
dashboard, not just execute."* This section extends §1–§5; where §7 and §9 conflict, §9's
explicit allowances win, everything else in §7 still binds.

### 9.1 Depth by tone, not borders

Three surface levels, composed by NESTING, separated by tone shift first and hairline second:

| Level | Token | Use |
|---|---|---|
| 0 recessed | `--page` | the page itself |
| 1 section | `--panel` | section cards (radius `--radius-xl`, `--shadow-sm`) |
| 2 inner | `--soft` / `--soft-2` | tiles, rows, tracks INSIDE a section |

- A section card whose fill differs from the page may DROP its border entirely (shadow + tone
  carry the edge). Inner tiles keep at most a `--hair-2` hairline.
- Radius escalates outward: controls 9–10px → inner tiles `--radius-md` → section cards
  `--radius-xl`. Never the reverse (an outer surface must never be sharper than its child).
- Section headers use the SectionHeader anatomy (9.3) — uppercase tracked label led by an
  IconTile — replacing bare `<h3>`s.

### 9.2 Gradient doctrine — gradients are data ambience, never decoration

New tokens (light + dark tuned, in globals.css):

```css
/* bottom-rising heat glows; ~18-22% domain color into transparent */
--heat-work:   linear-gradient(170deg, transparent 38%, color-mix(in srgb, var(--work-fg) 20%, transparent));
--heat-seam:   linear-gradient(170deg, transparent 38%, color-mix(in srgb, var(--seam-fg) 22%, transparent));
--heat-memory: linear-gradient(170deg, transparent 38%, color-mix(in srgb, var(--memory-fg) 18%, transparent));
--hatch: repeating-linear-gradient(45deg, transparent 0 3px, color-mix(in srgb, currentColor 22%, transparent) 3px 6px);
```

**Per-theme tuning (implemented Wave D).** The light values above read as intended over the
near-white `--page`/`--panel`. On the near-black dark substrate a translucent hue reads as
*emissive* (adds light), so the dark block re-tunes the percentages for even glow strength, not
glare: `--heat-work` **20%→24%** (blue sits dim over `#12171F`), `--heat-memory` **18%→22%**
(violet is the least-luminant hue on dark), `--heat-seam` **22%→20%** (amber is high-luminance —
trimmed so it glows, not glares). `--hatch` is `currentColor`-based and needs no per-theme delta.
The glows are exposed as `.heat-work` / `.heat-seam` / `.heat-memory` classes (not arbitrary
utilities) so the one-glow-per-viewport rule stays greppable.

Placement rules (hard):
1. A glow appears ONLY where the system is genuinely hot: the briefing attention hero
   (`--heat-seam` when Class-A gates wait, `--heat-work` otherwise), the one running/beating
   element in a view, a KPI hero tile. Never on static chrome, nav, or settings.
2. **At most ONE ambient glow per viewport**, plus the live pulse dots. If two things compete,
   the higher attention class wins and the other falls back to flat.
3. `--hatch` marks capacity/projected/secondary series in charts (never a fill for real values).
4. Reduced-motion never affects gradients (they are static); theme flips re-pick them via the
   domain vars automatically.

### 9.3 The reusable atoms (kit additions — build once, compose forever)

| Component | Anatomy | Use |
|---|---|---|
| `IconTile` | rounded-square (radius-sm) tinted chip: `*-soft` fill + `*-fg` 15px glyph; sizes sm 24 / md 32 | leads every SectionHeader, KPI, knob row, feed item |
| `SectionHeader` | IconTile(sm) + UPPERCASE tracked `--text-xs`/700 label + right slot (SegmentedControl / menu / chip) | every section card's first row |
| `DeltaPill` | ↗/↘ glyph + tabular value; good-soft/crit-soft tint; `title` = comparison window | KPI deltas, burn-down counters |
| `SegmentedControl` | pill-track of 2–5 options, active = panel-fill + shadow-sm; arrow-key navigable | in-card lens switching (time ranges, Gross/Net, Board/Pipeline). CLIENT VIEW STATE ONLY — never a governed act |
| `TooltipCard` | Radix HoverCard: panel surface, `--shadow-lg`, radius-md, 12px pad; title row → series rows (dot + label + mono value) → hairline → derived line (delta/margin) | hover on every chart mark, dot-matrix cell, capsule column |
| `DotMatrix` | waffle grid of radius-xs cells; intensity ramp `--soft-2` → `*-soft` → `*-line` → `*-fg`; cell hover → TooltipCard | density over time: attention age, activity coverage, verdict distribution |
| `CapsuleColumn` | vertical capsule: full-height `--soft-2` capacity track + filled value segment (domain/state fill); optional `--hatch` segment for projected | budget vs spend, lane capacity, queue depth by day |
| `SegmentedProgress` | count pill ("2/4") + n discrete segments: done = good-fg, active = work-fg + pulse, rest = `--soft-2` | staged processes: envelope stage position on cards, batch apply progress, phase completion |
| `DateTile` | stacked mono block (day / month), soft fill, radius-sm | leads event rows: receipts, scheduled loops, transitions |
| `MetaCount` | 15px icon + tabular count, muted; `title` expands | evidence refs, member counts, run counts on cards |
| `AvatarTile` | initials tile, deterministic identity tint (hash → one of 6 pastel pairs) | executors, harnesses — identity that survives renames |
| `ProcessPanel` | context row → input → ONE primary CTA → result chips streaming in (`--dur-slower` fade/slide) | the generator shape; THE contract for the future ai-console `proposeView` surface — suggestion chips are registered-view invocations / action proposals, acts stay governed |

Composition law: **screens are assembled from these atoms; a new screen introducing a bespoke
one-off where an atom exists is a review reject.** New atom → this table first, then kit, then use.

### 9.4 Interactivity doctrine

- Hover reveals depth, never hides essentials: every chart mark, matrix cell, and capsule answers
  on hover with a TooltipCard; rows lift 1px + `--shadow` (`--dur-fast`).
- Anything that opens something says so (chevron / "Open trace" / underline-on-hover mono id) —
  no mystery-meat interactions.
- Lens switches are instant and local; they never refetch governed state and never mutate.
- Motion budget per viewport: 1 ambient glow + live pulse dots + hover physics. Nothing else moves
  at rest. (Reduced-motion: pulses become static rings, hovers instant, glows stay.)

### 9.5 Settings & knobs — the three-kind model (extends the Settings IA)

Every tunable the system has (or will grow) is exactly one of:

1. **Authority view** — file-owned truth (policy.yaml, lanes, criteria, lens prompts). Read-only
   card that says in one plain sentence what the setting controls, its value now and who changes
   it ("changed only in its settings file; nothing in this console changes it"). Raw keys and the
   exact command sit behind a closed "For agents" disclosure (`ForAgents` in
   `components/settings/primitives.tsx`); a command with a blank such as `<file>` is never shown.
   Never editable in-console.
2. **Governed toggle** — one-click through the decision registry with receipt + undo path. None
   is built yet, so no card shows this kind. Stopping all agents (`policy autonomy-halt`) exists
   only as a command: the Settings card states whether agents are stopped, says stopping them
   from here is not built yet and what the stop does, and shows no button or type-to-confirm.
   Standing permissions open with one sentence ("Agents may do N things without asking; M of them
   cannot be undone"), list those in force with the ones that cannot be undone and never expire
   first, and fold expired ones behind their count.
3. **View preference** — client-only (theme, density, default lenses, collapsed sections).
   localStorage, provenance chip `local`, zero governance.

Clustering law: blast-radius group FIRST (the seven danger-first Settings groups), domain second.
A future feature adds knobs INTO an existing group; a new top-level group requires a new danger
tier (rare, deliberate). Knob row anatomy: IconTile + name + current value (mono when machine) +
provenance chip + right affordance (copy / governed act / toggle).

**Process settings render as recipe cards**: inputs (what feeds it) → the knob values → outputs
(what it produces), each a mono chip linking to its owner surface. A setting reads as a segment
of a live pipeline, not a dead form field — e.g. memory-engine profile shows
`sources → engine(knobs) → recall quality (bench hit@5)` with the real current numbers.

### 9.6 Information flow — why the dashboard is worth looking at

The console's spectacle is the organism visibly living. Representations that carry it:

- **Briefing pipeline strip** gains per-stage counts + flow direction; the attention hero carries
  the one heat glow. The fold answers "is it alive, and where am I needed" in one glance.
- **Transitions feed as the learning ticker** — move_type chips streaming in (slide-in on
  genuinely new events only).
- **DotMatrix of attention age** (needs-you items by age × class) — the queue's shape, not just
  its count.
- **CapsuleColumns for lane traffic/capacity**, hatched projections; **SegmentedProgress on every
  work card** (stage position); **TooltipCards everywhere** so hovering rewards curiosity.
- Every number carries its DeltaPill and its FreshnessChip — a value without delta + provenance
  is undressed.

## 10. Experience architecture — simple first, depth on demand

The current seven-item left rail is the stable operator map. Preserve its labels and order unless
an explicit operator decision changes them. A backend object, command, projection, or route does
not earn persistent navigation merely because it exists.

This section is the canonical information-architecture contract for console audits, mockups, and
implementation. Existing deep links remain valid, but appear as contextual secondary navigation
inside the surface that owns their operator question.

| Surface | User question | Routes |
|---|---|---|
| **Today** | What deserves my attention now? | Briefing |
| **Live** | What is observed moving, waiting, stale, or unreachable right now? | Overview, Harnesses, Coordination |
| **Work** | What is moving, blocked, ready, or waiting for proof? | Pipeline, WorkEnvelopes, traces, Receipts, Transitions |
| **Decide** | Which consequential calls need evidence-backed judgment, and what authority applies? | Decision/review queue, contextual authority inspector |
| **Knowledge** | What does Compound know, why, and how current or contested is it? | Memory, claims, positions, patterns, Sources |
| **System** | What capabilities, routes, health, policy, and authority configuration are effective? | Map, Routing, Settings, View grammar, Standing law |
| **Outcomes** | What shipped, changed, and fed learning back into the system? | Feedback loops |

### 10.0 Context ownership contract

Each fact has one primary surface. Other surfaces may show its identity, freshness, and a
decision-relevant summary, then link to the owner; they do not create a second interpretation.

- **Today ranks and links.** It owns cross-domain attention and recently proven change, not the
  full Work, decision, knowledge, or system record.
- **Live owns observation.** It distinguishes WorkEnvelope state, RunAttempt state, heartbeat,
  executor identity, lease state, and coordination reachability. `running` alone never means
  heartbeat-backed or genuinely active.
- **Work owns the governed unit.** The WorkEnvelope is the primary operator object for lifecycle,
  ownership, ActionIntent, PolicyDecision, execution evidence, Receipt, Transition, and the next
  proof required.
- **Decide owns judgment.** It shows the question, available evidence, known or explicitly unknown
  consequence, reversibility, blast radius, applicable authority, and the one safe action.
- **Knowledge owns meaning and provenance.** It shows claims and candidates with sources,
  evidence, freshness, confidence when projected, contention, and adjudication state.
- **System owns technical and configuration depth.** Capability graphs, routing/provider traffic,
  hooks, projection health, raw registries, settings, and the complete standing-law/authority view
  live here. Work and Decide may open a contextual authority inspector for the active object.
- **Outcomes owns external feedback.** Product signals, hypotheses, reviews, and learned next moves
  belong here; an internal Receipt is not an outcome merely because work completed.
- **Search is the one global overlay, not a destination.** It finds pages, apps, decisions and work,
  and its quick views replaced the Ask Compound box; it never invents authority or duplicate state,
  and nothing floats over page content.

Cross-surface navigation preserves object identity: a Live row opens the same WorkEnvelope in
Work; a Today seam opens its decision or work owner; evidence opens its Knowledge source; authority
opens the relevant System record. Before adding context to a screen, name its primary owner and
the operator decision the duplicate would enable. If neither is clear, link instead of copying.

### 10.1 Progressive disclosure contract

Today and Live support four presentation depths: **Glance → Understand → Investigate → Technical**.
Depth is a local view preference persisted in the browser (and mirrored into a cookie so the
server can see it). It may hide or reveal presentation regions, but it MUST NOT change source
truth, ranking, policy, or action authority. Its one effect on reads: a section hidden at the
current depth (home doctor on Today, coordination on Live) does not run its read at all. Other surfaces keep their task-specific density until they have an equally strong
progressive-disclosure model; a global depth switch that unpredictably hides working UI is worse
than explicit depth on the two overview surfaces.

### 10.2 Truthful Live visualization law

Live renders a harness → WorkEnvelope graph from the executor, RunAttempt, traffic, and
coordination projections. Configured harnesses are not activity. An agent is one agent key
(`lib/harness-label.ts`): the console regroups the executors projection's rows by the key of who
picked each item up, else its owner, which is the key the Work filter `owner=` compares, so an
agent's count equals the list its link opens. Work nobody holds is "No agent" and work you hold
is "You"; neither is counted as an agent. Working now, All agents and Today say the same
sentences from `lib/counts.ts`, and every number links to its Work list. A node or edge animates only
when a run record (RunAttempt or heartbeat) says an agent is running it now; the work record's own
state, `running` included, never animates. While the console cannot read run records nothing on the
page moves, and the answer says so: the page shows the work records as last read, not a live feed.
Each item shows when it was picked up and is marked "not moved in a week" past the shared stale
rule (`lib/work-state.ts`). Summary counts orient the operator, then the workfield is the
**Understand-level hero**; the latest work picked up (newest first, the only real time open work
carries, next steps in plain words, each row linked to its page) belongs to Investigate. Each agent
shows at most its two most urgent items (on hold, then not moved longest) while keeping its exact
totals as links, and the canvas draws at most eight agents; the rest are named on All agents. The
canvas fits in view on load, the scroll wheel scrolls the page, and there is no minimap. Codex,
Claude, Scripts, and Switchback/API retain distinct semantic identity colors. Selecting a node
updates a persistent inspector beside the workfield; deeper coordination and delegated-session
identity remain below the canvas.

The canvas is a projection, never an authority surface. It does not infer work ownership from
layout, invent activity to make the system feel alive, or persist prompt content.

### 10.3 Generative view boundary (Tambo-ready)

The read-only composition seam (`/api/ask`; no page calls it since Ask Compound's box became Search's
quick views) may translate natural language into registered view invocations. The model composes
the view; the registry owns rendering and every act. The current deterministic AI sandbox is a
replaceable adapter boundary for Tambo or another model runtime. Replacing it MUST NOT grant the
model mutation authority: registered actions still cross the existing policy, confirmation,
receipt, and transition seams.

The composer is route-aware: Today, Live, Work, and Decide each supply prompts that match the
question the operator is already trying to answer. It opens as a wide explanation workspace,
shows loading, result, and failure states, and never displaces the source projection or hides the
registered owner of an action.

### 10.4 Work starts with intervention

Work leads with what is stopped: the header names work on hold and work not moved in over a
week first, then splits "in progress" into running, picked up and not started, and waiting for a
proof check. Every number links to the List filtered to exactly those items (`filter=blocked`,
`stale`, `progress`, `running`, `claimed`, `verifying`, `all`); the stale List is oldest first.
The List's default filter is "On hold or not moved" (on hold, plus stale), led by on-hold items.
It initially exposes 18 rows and expands deliberately. Filter, agent, app or group and search
text live in the address. Every card and row links to the item's own page; a click elsewhere on
it opens a short preview with the same recorded reason and "Open work page". Stages, Flow,
By agent and Work packets are retired; their old `lens` values open the board.

The board and the List use the work item's state for filters, priority, color, status badges and
accessible labels. Run status appears separately as execution evidence: a successful attempt
does not complete work awaiting verification, and a running attempt does not override a held
work item.

## 11. Workspace refresh (2026-11-12)

The operator supplied two references: a task-board workspace and a performance-overview dashboard.
These deviations from §1–§10 follow from them.

- **Shell.** The sidebar sits on a quiet frame (`--page`). The page lives in one white, rounded,
  hairline panel (`.shell-panel`). The sidebar order is workspace, search (opens ⌘K), then the six
  sections (see "Console clarity"), with the apps under Apps. Every route stays one click away, as
  a section or a tab. Autonomy, freshness and instance identity move from the top bar into the
  sidebar status card. Nothing floats over the content.
- **Neutrals.** Light `--page/--ink/--muted/--faint/--hair` move from blue-grey to faintly warm
  grey. Dark mode is unchanged.
- **Type.** Page titles use a new `--text-3xl` step (34px). A `PageHeader` stat strip reads
  "26 open · 2 blocked", with a bold number and a quiet label.
- **Buttons.** The default primary is solid ink. Work-blue stays available as `tone="work"`.
- **Work.** The board is the default view. Columns are soft trays with a coloured state dot and a
  count, in this order: Suggested · Ready · In progress · Needs you · In review · Done. Cards show a
  state chip, the app (from `canonical_surface`), the title, the next step, stage segments, the owner
  avatar and the age.
- **Growth.** There are three new pages: `/apps` (portfolio), `/growth` (25-area audit matrix) and
  `/apps/[slug]`. All read `compound product growth --json`. Chart series use `--viz-1` (orange) and
  `--viz-2` (blue), which pass the dataviz palette validator in both themes. A day with no data is
  drawn as a gap, never as zero.
- **Blind is soft.** In the audit matrix and on coverage bars, a blind area is a pale red fill. When
  most of the portfolio is blind, the few measured cells must stand out, and the absence must stay
  visible without shouting.

### 11.1 Growth catalog pages (2026-11-13)

The growth read moved to `compound.product_growth.v0.2`: a catalog of areas in 12 groups, a product
profile that decides which areas apply, and ranked next moves. These rules replace the 25-area
matrix and the pale red fill above.

- **One question first.** `/apps/[slug]` is a short briefing about one app, with its own purpose
  line and no header button: what needs you (its work on hold, a real verdict due), how it is
  doing (dated numbers), all of its open work (the shared work-to-app index, on hold and not moved
  first, "See all N in Work"), the next moves (at most five, in the CLI's order), whether the
  changes helped, its ads, where it lives and runs (code, hosts and calls from the product graph,
  matched by every id spelling, observed or declared, with the observation's age), then what
  the product is and the 12 groups. Each move says what to do, why in one line and
  what it unblocks by name. The section says when its steps are chores most apps share
  (`sharedMoves`) and that nothing starts them on its own; there is no copy button, and workflow
  and skill names sit in a closed "For agents" disclosure. `/growth` opens with "Where
  are we blind across the apps, group by group?" and a two- or three-sentence answer computed from
  the projection. Everything else sits below and is quieter.
- **Never the full grid.** `/growth` draws 12 group rows × apps. One group opens at a time
  (`?group=<id>`) into its areas × apps. The app page shows the 12 groups as closed `<details>`;
  only groups holding a next move start open. `#group-<id>` and `#area-<id>` anchors open their
  section on load.
- **A move marks every area it covers.** A folded refresh (one stopped source, several areas)
  carries all of them in `area_ids`: each covered area gets the "Next move N" badge and the grid
  dot, and each group holding one starts open. Its meta line says "3 areas" instead of the first
  area's pipeline and id, which would speak for all of them.
- **Numbers carry their days.** Every tile, chart and funnel step names the days it covers
  (`kpiPeriod`, `seriesPeriod`, `funnelPeriods` in `lib/growth.ts`): the exact range when the
  CLI reports `window_start`/`window_end`, otherwise "last 30 days · newest data <day>", never a
  guessed range. A page shows one 30-day proceeds figure: the ranking's sentence is shown only
  when it carries its window, else it is rebuilt from the tile. Funnel steps that do not say their
  days are not drawn as one funnel (no arrows, a plain sentence why); a `partial` step reads "at
  least". One name per number on every page: "New downloads", "Ad installs" (Apple counts
  re-downloads), "Cost per install" (never "CPA"). Ad tiles on an app page open its Ads view.
  On Ads a change's colour follows what the number means (`ADS_GOOD_DIRECTION`: fewer installs is
  red, less spend is green); a campaign is active only with spend or impressions in the last 3
  days, else "quiet since <day>"; the campaign table comes first, apps link to their page, and
  platforms with no report fold into one closed line. A command with a `<placeholder>` is never
  shown; a runnable one sits in a closed "For agents" disclosure.
- **One results card per app.** Results rows fold into the catalog's app by every id spelling
  (`foldLoopsByApp`, `lib/products.ts`), named by the catalog; apps with no record are named in
  one line. A card names the change, what it should do and the days it is watched, and each
  reading carries its date. "Verdict due" means a real expected effect was written
  (`verdictDue`): a machine draft with a placeholder effect never waits on the owner. A due
  verdict links its exact decision (`decideHref`), never the general Decide list. The app page
  shows its own card under "Did the changes help?" and puts a due verdict under "Needs you".
- **Gaps are background.** "No data yet" is a pale neutral fill, the calmest filled state. "Not on
  file" is a dashed outline or hatch, "Set aside" a hatch on soft, "Does not apply" is left empty.
  Measured (green), out of date (amber) and the next-move mark (an ink dot or ring) are the only
  strong marks. A group cell prints a number only where something is measured; "0 of 5" would read
  as a zero.
- **Words.** Labels say "No data yet", "Partly measured", "Out of date", "Not on file", "Set aside",
  "Does not apply" and "Needs <area titles> first". Area ids appear only as small secondary mono
  text. A profile dimension that is not on file is a dashed "Not on file" chip, and the page says
  once what filling it in would change ("5 areas cannot be judged until the objective is on file").

### 11.2 The four main pages answer one question each (2026-11-13)

Today, Work, Decide and Live open with their question as the page title and the answer right under
it, in one to three plain sentences. Everything else sits below and is quieter; deeper panels stay
behind the depth control. These rules replace the §10 table's questions for these four pages and
the §3 empty-state doctrine copy ("Clean seam…", "Propose-only holds the line…").

| Page | Question | First answer |
|---|---|---|
| Today | What needs you today? | "N decisions wait for you", counting only what is his (`waitingOnYou`), then at most five of them in the order Decide shows (`yourDecisionGroups`), so Today's rows are Decide's top rows; each row says what Decide's row says (the app it is about, the record's words, the kind's question and how long it has waited, from one function, `decisionHeadline`) and has one way in. No row carries a dead-end reason or an aggregate "cannot be undone" label. Quiet lines name what is not counted: decisions an agent must word first, records closed without him, work waiting on others, and the skill proposals folded out as agent housekeeping. Then, once, how a decision moves today (`HOW_DECISIONS_MOVE`). When nothing waits, it says so and "What the agents are doing" becomes the answer. |
| Work | What is being worked on, and what is stuck? | Work items marked in progress out of open work, the split, what is stuck (picked up 7+ days ago and still open; on hold), and work items waiting for a decision. |
| Decide | What is waiting for your decision? | The same "N decisions wait for you" as Today and the sidebar, the same quiet lines (here "closed without you" opens its reasons and next steps), and once per page how a decision moves today: he tells an agent in chat, the agent records it and carries it out, and deciding on the page is being built. No button, "connect" step or command is offered as a way to decide. Only his decisions are cards, in Today's order, the unworded ones last; proposals to update agent skills are one quiet line outside the count. Each card names the app (linked) or thing it is about, asks its kind's question in one sentence, and says what a yes and a no would cause and whether it can be undone, from `decisionKindWords` in `lib/decision-registry.ts` (the one owner of those words); what the record does not say is named once ("The record does not say …"), never as empty boxes. Held work lists each item inline: title, app, recorded reason, since when, linking its page. The record behind a card sits in a closed "For agents" disclosure. |
| Live | Who is working on what right now? | Agents holding work, the in-progress split and age, and a plain note when live run records are not visible, so "running" is the work record's word. |

- **Words.** `lib/plain-words.ts` owns the names (decision, item, work item, agent, in progress,
  on hold, waiting for a proof check, picked up, filed, watching), the plain area labels (an area
  path like `source-control-cleanup/scr_….preimage` reads "Repository cleanup"; the id is small
  secondary text), the plain reasons and next steps, and `BANNED_PHRASES`. The wired
  work-legibility smoke runs `scripts/plain-pages-cases.mjs`, which renders the four pages against
  a synthetic binary and fails on any banned phrase or raw area path in the visible text.
  A work item has one title, from `lib/work-title.ts` (stated title, goal, success condition, else
  words derived from the record; never a generic "Work details"), short on cards and lists and
  whole on its page. An agent has one key and one name, from `lib/harness-label.ts` (`agentKey`,
  `agentDisplayName`): every spelling of one agent groups and filters as one. Links to Work, a work
  item, Decide, an app, Ads, a Settings section, Memory and evidence history come from
  `lib/links.ts`, and the Work page reads its parameters through `parseWorkListParams`.
- **Numbers.** `lib/counts.ts` holds one function per concept, with its unit in the label:
  waiting for you (decisions, items, work items), open work items, in progress (`claimed`,
  `running`/`in_progress`, `verifying`), picked up more than 7 days ago (from `claimed_at`: the
  projections carry no "last changed" time for open work), on hold (`held`, `blocked`), agents
  holding work, and decisions ready now. Pages and the sidebar call these, never their own count.
  "Waiting for you" counts only what is his: agent housekeeping (skill proposals), records with no
  clear question (`needs_wording`), what closed without him and work waiting on others are each
  named apart (`describeWaiting`) and never added in; a number the projection does not give is left
  out, never shown as 0.
  The rules for one work item (`isInProgress`, `isOnHold`, `isWaitingForCheck`, `isStale` and its
  threshold `STALE_AFTER_DAYS`) live in the client-safe `lib/work-state.ts`; `counts.ts` counts with
  them and re-exports them, so a client component asks the same question without its own copy.
- **Send only what is shown.** Decide sends the full detail of the open decision only
  (`?decision=<key>` re-renders with that one's detail); filed is a count plus one row per kind
  (`?filed=<kind>` opens its newest 100). Live's timeline gets its six rows, and the list of
  agent-started sessions renders only at the Technical depth.
- **Decide standing (2026-11-13).** A button needs owner evidence read in the last 25 seconds;
  that gate is unchanged. The list (`attention --json`) takes about a minute to read, so the
  opened decision also reads its own area (`reviews --json --area <area>`, about a second, the
  read the action path resolves against) through the non-blocking read, and that copy is its
  evidence when it is fresher than the list's. It applies to that one decision only. Since no
  decision can be recorded from the browser yet (2026-11-17), neither rows nor cards show that
  standing: no "Ready to review" / "Needs a closer look" sections, no "Not yet" or "Checking…"
  badge and no banner, so a recheck never moves, regroups or rebuilds anything. The list is one
  list in the one shared order; the open card is keyed by its decision, so an open disclosure or
  typed filter survives a recheck. A link to a decision that is not listed says why (answered or
  closed, or a skill proposal folded out as housekeeping) and offers the filed list; the first
  decision still opens beside the note, and the address keeps the link.
- **Work views.** Three views: the board by state (default), the List, and By app. Board
  columns: Suggested · Ready to start · Running or picked up · On hold · Waiting for a proof check
  · Done, with On hold moved to the front whenever it has items; the two in-progress columns add
  up to the "in progress" number. A card names its app and its agent on one line, and a title that
  repeats in its column carries a hint that tells the cards apart.


## Console clarity and bounded reads

- `lib/experience.ts` owns every name a person sees for a place. The sidebar is
  six sections named after his questions (Today, Apps, Work, Agents, Knowledge,
  Settings) plus Search, with his apps listed under Apps and the app he is on
  always shown. A section's pages are one row of tabs at the foot of the page
  header, so each stays one click away; a one-page section has no row. The
  breadcrumb reads section › page › item. A page that leaves the menu stays in
  the registry as unlisted, so its address and breadcrumb keep working; pages
  agents use sit in Settings' quiet back room. The sidebar has one count: what
  waits for him, on Today; no page counts on groups and no Work count.
  Search finds every page by name, earlier name and purpose, every app by name
  and alias, waiting decisions by the words Today shows and open work by title.
  It reads the copies the pages already cache once, when it opens; typing never
  starts a read, a read still loading says so, and it offers no terminal commands.
  Use `PageHeader` for the page label, a short lead or answer, and named freshness.
  An item or a question may be the title; the section's name stays above it.
- Decide leads with the proposal and opens on **Next step**. Choices, status,
  history and evidence remain available without repeating the same blocked
  sentence on every row. Search matches the displayed area names; area filters
  combine equal display names and preserve the underlying decision identities.
- Owner readiness and browser access are separate facts. The standard browser
  is read-only. Show inspect commands and owner links instead of a disabled
  form or advice to relaunch with more permissions. A complete denial or even
  a partial authority bundle must never be treated as absent authority. A live,
  singleton, revision-bound registry workflow can describe the owner's options;
  this does not grant the browser permission to execute them.
- Settings opens one section through `?section=`. Unopened panels do not render
  their bodies. Settings, Feedback loops and Standing law use the existing
  background read queue, preserve last-good/failure/loading states, and refresh
  while reads are pending. Unknown counts are shown as unknown, never zero.
- Loop cards summarize observations, hypotheses and reviews. Metric disclosure
  shows the latest observation per provider and metric; it does not render the
  whole event history. A rising number is not automatically a good result.
- Counts name their population: registered apps, apps with data, decisions,
  underlying items, agents and active work have different meanings. Do not
  force different populations to have the same total.

The existing read-queue, plain-pages, decision-model and decision-feedback checks
exercise these seams, including cold page reads and stale or partial authority.


## Page journeys (supersedes question-title and read-only fallback copy above)

- Pages lead with their registry name (the same words as their tab), a short
  purpose or current answer, and at most one header button. `lib/experience.ts` owns default purposes
  and destinations; `PageHeader` renders them. Dates and read status are secondary.
- A page has a header button only when it leads somewhere he is not already
  looking: another page, a filtered list, or a part far below the first screen.
  No button scrolls to what is already on screen or repeats a tab of the same
  section; no button is better than a decorative one. In-page links land below
  the sticky top bar (`scroll-padding-top` on `html`). Empty and loading
  states retain useful destinations. Work filters are shareable and respond to
  browser navigation. Commands containing placeholders are never primary actions.
- Work details lead with the goal, assigned agent, latest recorded blocker and
  next step. `work-next-step.ts` reads the work record and its current lifecycle
  transition; cached board text cannot override the blocker. Missing retry and
  ownership evidence stays explicit. Technical detail remains behind disclosures.
- The standard browser remains read-only. A clipboard handoff says it copies a
  task and starts nothing. Never claim that operator mode enables browser writes,
  that unavailable actions are queued for repair, or that Park/Dismiss works
  without checking its actual authority.
- Tab names are words he would say out loud: All decisions, All apps, Growth
  gaps, Results, Ads, Working now, All agents, Waiting on each other, Memory,
  Rules, Sources, Models and cost. Earlier names (Decide, Portfolio, Live,
  Coordination, Routing, Map, Views) are aliases so `PageHeader name=` callers
  keep working; they are not shown. Supporting copy explains consequences and
  next steps. Internal identifiers stay in detail.

Coordination (Waiting on each other) also uses the existing background reader and refreshes while
waiting; a cold read must not delay rendering or appear as an empty list of agents. It shows one
paragraph, not a card per session: how many sessions wait, what most of them wait for in plain
words and since when (the dates of their last capacity readings), then the agents whose sessions
hold work, each linked to its Work list, and the same agent and in-progress sentences as Working
now. One freshness chip; recorded coordination entries sit behind "For agents".


## Connected project view

There is no Projects page. "Which app is this work for, and what is stuck there?" is
answered where the work already lives: Work's "By app" view (`/work?lens=project`) and
each app's own page. The app catalog (`product growth --json`, with its aliases) is the
registry for apps; the console adds none.

One resolver (`lib/work-project.ts`) places every open work item in exactly one group:
a catalog app, a codebase, an app the record names that the catalog does not list, or
"Not linked to a project". The record's own product id wins; then folders the inventory
says an app's code lives in (`code_in`), then folder and surface names. Task prose,
actor names and timing never create membership, and nothing is guessed when unsure:
work that names no app or codebase stays visible in "Not linked", sorted by the same
rule as every other group.

"By app" lists each group with "N need you · N in progress · N waiting for a check ·
N not moved in a week" from the shared item rules (`lib/work-state.ts`), groups that
need you first. Each count opens the List of exactly those items (`app=<slug>` for an
app, `project=<group id>` otherwise); an app row also links to its app page. The view
reads the same three projections the index owns (`readWorkProjectIndex`) and keeps
their loading, last-good and failed states: until all three hold a list, the grouping
is unknown, never empty, and a List narrowed to an app says it is still being read.

Old `/projects` addresses redirect: an app's scope to that app's page, an opened work
item to its own page, anything else to "By app".
