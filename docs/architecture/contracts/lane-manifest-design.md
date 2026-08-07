# Lane Manifest — design brief (launch owner × compound authority)

Status: **proposal — compound-only slice implemented (2026-08-07)**. Author: 2026-08-07
session. Consumer: compound governed adapters + the launch owner's profile generator. This
brief maps the current surface, proposes one declarative unit (the **lane manifest**), and
defines the authority split. The compound-owned half of the slice is landed: the manifest
schema (`internal/lanemanifest`), `compound hook install --lane-file` deriving harness +
roster, and the governed-spawn full-roster gate. The launch-owner half (manifest as launch
authority, `sb profile plan/apply` consuming it) remains a starting hypothesis to prove only
via a future vertical slice.

## 6b. Landed compound slice (2026-08-07)

The compound-only half of §6, implemented end to end:

1. **`internal/lanemanifest`** — the declarative unit. `Parse` (YAML/JSON),
   `Validate` (harness family, roster, schema version), `InstallHarness()`
   (lane → install harness via the lanes.json `hook_harness` convention).
   Reference manifest: `fixtures/lanemanifest/minimax.lane.yaml`.
2. **Roster selection** — `hookplan.RosterEventSet`: `minimal` = {SessionStart,
   PreToolUse}; `full` = {SessionStart, UserPromptSubmit, PreToolUse,
   PostToolUse, SessionEnd}. Bindings are narrowed to the roster; semantic
   posture validation reads the full posture. `InstallHooksForLane` + a
   roster-aware `InstallHooksWithRoster`; the pre-roster signature is unchanged
   (default full).
3. **`compound hook install --lane-file <manifest> [--roster minimal|full]
   [--check]`** — derives the install harness and roster from the manifest;
   `--roster` overrides. The default flips: `minimal` needs zero ceremony, `full`
   is the governed selection.
4. **Governed spawn gate** — `work advance --execute` refuses a hook-governed
   lane whose observed settings do not cover the full roster (`gateRefusal`,
   fail-closed, read-only; never installs on spawn). Lanes without an
   observable profile are governed by tuple preflight + permission mode instead.
5. **Two-writers regression** — the NO-CLOBBER reconciler already preserves
   every non-compound settings key; tests now pin that contract for the
   launch-owner-regen → drift → re-apply loop.

The launch-owner half (manifest as the launch/capture/route authority) is unchanged
and still requires the cross-repo slice.

## 1. Problem

Setting up a harness lane today is a many-piece ceremony:

- launch owner: launch profile (`sb profile apply` → wrapper, `settings.json`,
  `harness_settings`, capture policy, tap/Headroom bindings).
- compound: per-harness hook roster (`compound hook install --harness …`), isolated
  `--config-dir` profiles per lane, `install-lane.sh` glue mapping lane → hook harness,
  plus hand-written settings blocks that the launch owner's conformance regen can revert
  (`find_bc58676341a9`).

Consequences: every new harness × lane combination re-derives the ceremony; hook fixtures
(PreToolUse/PostToolUse/FailedFirst/session-end/stop) are prerequisites instead of
refinements; the same provider lane can be reached as an API endpoint, a tap, or a harness —
but nothing expresses "this lane is the same thing as that endpoint" in one place.

## 2. Current state map

| Concern | Owner today | Artifacts |
|---|---|---|
| Provider lane (model, upstream) | launch owner provider registry | `provider-registry.json`, routes |
| Launch profile (harness × lane × capture) | launch owner | wrapper, `settings.json`, capture policy, tap/Headroom |
| Harness authority hooks (governance) | compound | `hook install` rosters per harness type |
| Lane identity / executor roster | compound | `compound executors`, lease/attempt state |
| Lane-as-API | launch-owner gateway | OpenAI-compatible endpoints, taps (`:1877x`) |

Two authorities, one seam: compound **consumes** the launch owner's published projections
(profiles, account projections, lane conformance); the launch owner never reads compound
state. The seam today is implicit (paths, env, wrapper ids) — no schema names it.

## 3. Target model: one manifest, two projections

**Lane manifest** (tracked source in the launch owner's repo as the authority; compound
consumes it via `--lane-file`):

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

- **launch owner** owns: wrapper + `harness_settings` + capture policy + tap/Headroom
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
- launch owner: meaning-neutral launch/capture/route truth (it owns the wire).
- compound: governance truth (it owns what harnesses may do, and the receipts).
- compound never mutates launch-owner state; the launch owner never reads compound state.

## 5. Generation rules

1. `sb profile plan --lane <manifest>` validates the manifest against provider registry +
   account projections; `sb profile apply` emits the artifacts.
2. `compound hook install --lane-file <manifest>` derives harness + roster from the
   manifest; `--config-dir` gating stays (isolated profiles per lane).
3. A manifest change is a **binding**: re-apply + first real launch after apply is the proof
   (launch args are a binding — existing doctrine, unchanged).
4. Retirement: the manifest replaces hand-written `install-lane.sh` glue and the
   implicit path coupling; retire those once the slice proves the consumer path.

## 6. Slice proposal (when approved)

Vertical slice: **claude-minimax as both harness and API from one manifest.**

1. Manifest tracked in the launch owner's repo; `sb profile plan`/`apply` consumes it (or a
   `--lane-file` passthrough — decide in slice).
2. Compound `hook install --lane-file` derives the minimax roster (claude-minimax hook
   harness per the lanes.json mapping) — `minimal` default, `full` on governed spawn.
3. Prove: `claude-minimax --version` works from a fresh profile; a governed
   `compound work advance --execute` on the minimax lane passes readiness with `full`
   roster; the same lane serves an API call through its tap route.
4. Tests probe depth: manifest drift (provider model removed from registry), roster
   mismatch (lane says minimal but governed spawn requires full), and the two-writers
   regression (launch-owner regen must not clobber compound's applied settings).

## 7. Open questions

1. Manifest source of truth location: the launch owner's repo (owner of launch) vs compound
   (owner of governance) — recommendation: the launch owner's repo, compound consumes with
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
