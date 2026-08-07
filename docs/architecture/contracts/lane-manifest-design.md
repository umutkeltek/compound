# Lane Manifest — design brief (switchback × compound authority)

Status: **proposal — not implemented**. Author: 2026-08-07 session. Consumer: compound
governed adapters + switchback launch-profile generator. This brief maps the current surface,
proposes one declarative unit (the **lane manifest**), and defines the authority split. It is
a starting hypothesis; implement it only via a vertical slice that proves the path.

## 1. Problem

Setting up a harness lane today is a many-piece ceremony:

- switchback: launch profile (`sb profile apply` → wrapper, `settings.json`,
  `harness_settings`, capture policy, tap/Headroom bindings).
- compound: per-harness hook roster (`compound hook install --harness …`), isolated
  `--config-dir` profiles per lane, `install-lane.sh` glue mapping lane → hook harness,
  plus hand-written settings blocks that switchback's conformance regen can revert
  (`find_bc58676341a9`).

Consequences: every new harness × lane combination re-derives the ceremony; hook fixtures
(PreToolUse/PostToolUse/FailedFirst/session-end/stop) are prerequisites instead of
refinements; the same provider lane can be reached as an API endpoint, a tap, or a harness —
but nothing expresses "this lane is the same thing as that endpoint" in one place.

## 2. Current state map

| Concern | Owner today | Artifacts |
|---|---|---|
| Provider lane (model, upstream) | switchback provider registry | `provider-registry.json`, routes |
| Launch profile (harness × lane × capture) | switchback | wrapper, `settings.json`, capture policy, tap/Headroom |
| Harness authority hooks (governance) | compound | `hook install` rosters per harness type |
| Lane identity / executor roster | compound | `compound executors`, lease/attempt state |
| Lane-as-API | switchback gateway | OpenAI-compatible endpoints, taps (`:1877x`) |

Two authorities, one seam: compound **consumes** switchback's published projections
(profiles, account projections, lane conformance); switchback never reads compound state.
The seam today is implicit (paths, env, wrapper ids) — no schema names it.

## 3. Target model: one manifest, two projections

**Lane manifest** (`compound://registries/lane-manifest` served, tracked source in
switchback repo as the authority, compound consumes):

```yaml
lane: minimax
harness: claude-code          # claude-code | codex | opencode | api
model: minimax/MiniMax-M3
effort: xhigh
capture: segmented_full_wire  # metadata_only | segmented_full_wire | none
permission: bypass            # plan | acceptEdits | bypass | default (harness defaults)
api:
  enabled: true               # expose as OpenAI-compatible endpoint
  route: minimax/*            # gateway route pattern
hooks:
  roster: minimal             # minimal | full (default minimal)
```

Two projections are generated from one manifest, by their owner:

- **switchback** owns: wrapper + `harness_settings` + capture policy + tap/Headroom
  binding + gateway routes. The `api:` block maps directly onto existing route targets —
  "lane as API" stops being a separate config.
- **compound** owns: adapter wiring (exact `model:` on every Agent/Workflow spawn),
  hook roster, config-dir profile. The `hooks.roster` field selects the roster class:

| Roster | Hooks | When |
|---|---|---|
| `minimal` | session-start (authority attestation), pre-tool-use (landing/deny gates) | default — a lane works with zero hook ceremony |
| `full` | + post-tool-use, session-end, stop, user-prompt-submit (governance/telemetry) | opt-in refinement for governed dispatch |

The default flips: **hooks are refinements, not prerequisites.** `full` is selected
automatically when a governed adapter spawns a lane (`compound work advance --execute`),
so governance is preserved where it matters without burdening every interactive lane.

## 4. Combination semantics

One manifest expresses all three consumption modes:

- **API**: `api.enabled: true` → endpoint + route (existing gateway behavior, now derived).
- **Harness**: `harness: claude-code` → wrapper + isolated profile + hooks.
- **Both**: the normal case — the same lane is addressable as `http://127.0.0.1:<tap>` AND
  as `claude-minimax`. The manifest is the single place that names "this lane is the same
  thing as that endpoint".

Authority split (unchanged doctrine, now explicit):
- switchback: meaning-neutral launch/capture/route truth (it owns the wire).
- compound: governance truth (it owns what harnesses may do, and the receipts).
- compound never mutates switchback state; switchback never reads compound state.

## 5. Generation rules

1. `sb profile plan --lane <manifest>` validates the manifest against provider registry +
   account projections; `sb profile apply` emits the artifacts.
2. `compound hook install --lane <manifest>` derives harness + roster from the manifest;
   `--config-dir` gating stays (isolated profiles per lane).
3. A manifest change is a **binding**: re-apply + first real launch after apply is the proof
   (launch args are a binding — existing doctrine, unchanged).
4. Retirement: the manifest registry replaces hand-written `install-lane.sh` glue and the
   implicit path coupling; retire those once the slice proves the consumer path.

## 6. Slice proposal (when approved)

Vertical slice: **claude-minimax as both harness and API from one manifest.**

1. Manifest tracked in switchback repo; `sb profile plan`/`apply` consumes it (or a
   `--lane-file` passthrough — decide in slice).
2. Compound `hook install --lane-file` derives the minimax roster (claude-zai hook harness
   per current mapping) — `minimal` default, `full` on governed spawn.
3. Prove: `claude-minimax --version` works from a fresh profile; a governed
   `compound work advance --execute` on the minimax lane passes readiness with `full`
   roster; the same lane serves an API call through its tap route.
4. Tests probe depth: manifest drift (provider model removed from registry), roster
   mismatch (lane says minimal but governed spawn requires full), and the two-writers
   regression (switchback regen must not clobber compound's applied settings).

## 7. Open questions

1. Manifest source of truth location: switchback repo (owner of launch) vs compound
   (owner of governance) — recommendation: switchback repo, compound consumes with
   authority_ref + source_revision, mirroring existing projection consumption.
2. `permission` in the manifest vs compound-side policy only — recommendation: manifest
   declares default; compound policy can override per work class (governed spawns already
   derive permission from policy, not from the lane).
3. Does `api.enabled` imply the tap routes live with the harness (one manifest) or stay
   independent gateway config? Recommendation: derived from the manifest, one file.

## 8. Retirement conditions

- `adapters/_lib/install-lane.sh` hand-mapping retired once `hook install --lane-file`
  covers all lanes (minimax, zai, zai-direct, qwen, claude-codex, opencode).
- Implicit path coupling (wrappers pointing at repo-relative scripts) retired when the
  manifest is the only thing that names a lane's artifacts.
