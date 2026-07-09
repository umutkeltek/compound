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
  /* — type: 13px base, explicit line-heights, tabular for numerics — */
  --text-2xs: 10.5px;   --text-2xs--line-height: 1.4;    /* inline mono ids, boundary tags */
  --text-xs:  11px;     --text-xs--line-height: 1.45;    /* meta, section labels, pills */
  --text-sm:  12px;     --text-sm--line-height: 1.5;     /* secondary body */
  --text-base:13px;     --text-base--line-height: 1.55;  /* DEFAULT body */
  --text-md:  14px;     --text-md--line-height: 1.5;     /* card titles emphasis */
  --text-lg:  16px;     --text-lg--line-height: 1.35;    /* drawer title */
  --text-xl:  20px;     --text-xl--line-height: 1.2;
  --text-2xl: 26px;     --text-2xl--line-height: 1.1;    /* KPI value */

  --font-sans: -apple-system, BlinkMacSystemFont, "SF Pro Text", "Segoe UI", Roboto, system-ui, sans-serif;
  --font-mono: "SF Mono", ui-monospace, SFMono-Regular, "JetBrains Mono", Menlo, monospace;

  /* — radii — */
  --radius-xs: 6px;     --radius-sm: 9px;    --radius-md: 12px;
  --radius-lg: 14px;    /* the card radius */
  --radius-xl: 18px;    --radius-pill: 100px;

  /* — semantic layout sizes (the only "spacing" tokens) — */
  --rail-w: 52px;   --nav-w: 218px;   --drawer-w: 460px;   --read-max: 720px;

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
  --ink:#E4E9F0; --strong:#F4F7FA; --muted:#93A0AF; --faint:#66727F;
  --hair:#242C37; --hair-2:#1F2630;

  --work-fg:#5AA2FF;   --work-soft:#0E2436;   --work-line:#1D3E5C;
  --memory-fg:#A78BFA; --memory-soft:#1E1938; --memory-line:#362E5A;
  --cap-fg:#3FC8DE;    --cap-soft:#0E2C34;    --cap-line:#1D4750;
  --route-fg:#8A8CF7;  --route-soft:#191A38;  --route-line:#2E2F5C;
  --product-fg:#F472B6;--product-soft:#331023;--product-line:#571C3A;

  --good-fg:#33B679;   --good-soft:#10281E;   --good-line:#1E4331;
  --seam-fg:#D69B3C;   --seam-soft:#2A2211;   --seam-line:#48391B;
  --crit-fg:#E06E5C;   --crit-soft:#2C1714;   --crit-line:#4C2620;

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

**Type/mono rules.** Base 13px. Mono (tabular) is mandatory for: every machine id (`work_…`,
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
- **Freshness chip:** dot + mono label, `title`=source command. Fresh (<TTL): green `live · HH:MM:SS`.
  Stale: amber `stale · 4m`. Unknown: faint. Every card showing projected data carries one.
- **Empty state:** dashed hairline, `--page` bg, doctrine copy — never "No data"
  ("Clean seam. Nothing waits on you." / "Propose-only holds the line…"). Optional single CTA.
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
