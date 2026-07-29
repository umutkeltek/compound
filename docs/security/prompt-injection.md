# Prompt-injection boundary and upgrade plan

Status: implemented foundation plus long-term migration plan, 2026-07-27.

## Verdict

The pre-hardening harness was high exposure because attacker-controlled content,
private data, authenticated accounts, and broad mutation tools could coexist in
one model context. Model instruction-following was being asked to carry a
security property it cannot guarantee.

Compound's target is containment, not perfect prompt classification:

> Assume any model that reads attacker-controlled content can emit an
> attacker-controlled action request. The request still cannot acquire
> authority or reach a forbidden sink.

This preserves the existing operator experience:

- Codex remains `approval_policy=never` and `danger-full-access`.
- Claude remains `bypassPermissions`.
- actions inside standing grants execute automatically with receipts;
- routine reads, local verification, and reversible workspace edits remain
  automatic;
- holds are limited to deterministic boundary changes, consequential actions
  outside grants, and unknown/high-risk behavior;
- classifiers can only hold, deny, or quarantine. They cannot authorize.

## Threat scale

| Injection | Surface | Example |
|---|---|---|
| I0 | Authenticated operator | Direct first-party instruction |
| I1 | Visible indirect | README, web page, email, issue, tool output |
| I2 | Obfuscated or multimodal | Unicode, encoding, screenshot, PDF, OCR |
| I3 | Chained or laundered | Web → child → parent; output → compaction |
| I4 | Persistent | Memory, cache, raw corpus, scheduled/delayed trigger |
| I5 | Trusted-channel supply chain | AGENTS, CLAUDE, SKILL, plugin, MCP schema, hook |

| Impact | Meaning |
|---|---|
| S0–S2 | No effect, answer steering, bounded read/cost/denial of service |
| S3 | Confidential read or reversible mutation |
| S4 | Secret exfiltration, account/public mutation, durable poisoning |
| S5 | Production/authority compromise or self-propagation |

The release claim is zero unauthorized S4/S5 actions, not “the model never
follows an injection.”

## Model and harness map

Model names below are the lanes observed during the audit. Aliases drift; every
certification run must record the exact resolved provider/model revision.

| Harness/function | Observed model/lane | Prompt-injection authority |
|---|---|---|
| Codex interactive | GPT-5.6 Sol, xhigh | Full local/network tool surface; generated Compound hooks now monitor every tool action |
| Claude Code native | `opus[1m]` alias | Full Claude tool surface with bypass permissions; universal generated Pre/PostToolUse matchers |
| Claude alternate profiles | Z.ai, MiniMax, Qwen, GPT-5.6 profile | Same action monitor where the Compound profile adapter is installed; installation parity remains a required proof |
| Credential-bearing chat executor | GPT-5.6 with several fallback lanes | Existing command/cron checks, but not yet complete Compound source/sink coverage |
| Unsandboxed chat runtime | GPT-5.5 through an app-server | Full-tool reachability was the most exposed audited path; not yet fully enrolled |
| Chat-runtime compaction | GPT-5.3 with GPT-5.5 fallback | Derived transformation; native lineage enforcement remains future work |
| Compound distiller | Routing-gateway extraction alias | Strict JSON and candidate/no-promote; no direct authority-memory promotion |
| Compound judge/panel | Routing-gateway judge alias | Advisory only; model verdict cannot grant |
| Retrieval index | Deterministic retrieval, then caller model | Provenance is preserved; retrieved content remains data |
| Retrieval model work | Subagent lane plus embeddings | Probabilistic extraction cannot raise trust |
| Browser second-opinion lane | Browser-hosted frontier model | Advisory, no local action authority; provider confidentiality still matters |

## Input map

| Input avenue | Model consumer | Current boundary | Residual risk |
|---|---|---|---|
| Direct operator prompt | Active Codex/Claude model | `UserPromptSubmit` mints authenticated-operator source | Pasted/quoted external text must remain derived data, not operator authority |
| AGENTS/CLAUDE steering | Codex/Claude | Exact source hash; canon structural attestation; actions still monitored | Native harness reads repo instructions before Compound can isolate them |
| SKILL/plugin instructions | Skill-aware caller | Registered canon files receive surface/schema attestation; schema drift holds | Unregistered third-party bundles still need admission before activation |
| MCP descriptions/schemas | MCP-capable caller | Capability-graph schema revision binds connector actions; missing schema holds | Direct shell use of unmanaged gateways must be enrolled |
| Local files/repo docs | Main model or subagent | PostToolUse source envelope; later actions carry active lineage | A mixed context can still steer answers, though not governed action authority |
| Shell/test/build/git output | Main model | Tool result source plus universal next-action monitor | Unmanaged subprocesses can expose sensitive bodies before a broker exists |
| Web/search/browser DOM | Main/browser model | Born untrusted; reads/snapshots automatic; submits/uploads consequential | Authenticated browsing needs isolated reader and scoped executor |
| Screenshot/image/PDF/OCR/video | Vision-capable caller or browser second-opinion lane | Treated as untrusted result lineage | No dedicated no-secret multimodal reader yet |
| Email/calendar/Drive/docs/sheets | Caller through `gog`/MCP | Connector schema/destination/source binding; reads distinct from mutations | Every direct CLI path still needs executor enrollment |
| GitHub/issues/PR/logs | Caller/GitHub connector | External result lineage; read/create/update actions classified separately | Account-scoped credential broker remains future work |
| Other SaaS MCPs | Caller model | Unknown/missing schema fails closed; known actions bind exact operation | Schemas and credentials must be admitted and scoped per connector |
| Secret/env/keychain/browser profile | Tool-capable model | Secret confidentiality and allowed sinks; secret → network is denied | Credentials can still enter raw tool output without a secret broker |
| Curated brain | Recall/Compound → caller | Promoted provenance; authority writes require adjudicated sources | Staleness and earlier poisoned canon require re-adjudication on drift |
| Raw retrieval corpus | Recall caller | Evidence, not canon; active source lineage | Reader/planner separation is not complete |
| ContextPack/findings/decisions | Codex/Claude hooks | Control/result source with hashes and lineage; cannot itself authorize action | Prompt-role mixing can still steer prose |
| Compaction | Harness compactor | `SessionStart source=compact` collapses lineage without raising trust or losing parent refs | Native PreCompact capture is not yet registered |
| Subagent result | Parent model | Explicit `subagent_result` transformation born untrusted | Non-tool-native child lifecycle events are not all enrolled |
| Critic/provider output | Parent/merger | Advisory derived source | Can mislead planning; cannot relax monitor |
| Chat/Telegram/scheduled prompt | Credential-bearing chat executor or routed model | Partial local controls | Highest residual exposure until those harnesses use the same monitor |
| Package/install/build supply chain | Model plus host process | Install and unknown command classes hold; instruction manifests attested | Isolated build/install executor remains future work |

## Enforced action path

```text
ingress/result
  → SourceEnvelope(origin, trust, confidentiality, sinks, content hash, parents)
  → bounded SessionSourceStore projection
  → model proposes tool call
  → deterministic action classification
  → exact ActionBinding(args, destination, source refs, tool schema, policy)
  → security floor
  → standing grant or one-use exact approval
  → allow / allow_with_obligations / hold / deny
  → receipt and taint-preserving result
```

Least trust, greatest confidentiality, and the intersection of allowed sinks
survive transformations. Web and subagent results are born untrusted even when
their parent was an authenticated operator prompt. Compaction can reduce the
active projection, but it cannot erase parents or raise trust.

## Low-friction decision boundary

| Automatic | Hold | Deny |
|---|---|---|
| Local tests/builds and read-only inspection | Unknown tool/action | Invalid source lineage |
| Public web fetch and browser snapshot | Missing tool schema or destination | Source not allowed for requested sink |
| Reversible workspace write with allowed source | Consequential submit/upload/send/deploy outside grant | Secret/confidential data to forbidden egress |
| Connector read with exact schema | Instruction/MCP/plugin schema or lineage drift | Untrusted authority-memory promotion |
| Any exact standing-grant action | Suspicious classifier result | Malicious classifier result |

For `compound ...` shell actions, the executable command registry supplies the
outer classification and admission posture. The policy package does not keep a
second subcommand allowlist. A registered `governed` command enters the local,
reversible lane and must still enforce its own ActionIntent/PolicyDecision;
an absent command or a registry row marked `hold` hits the security floor.
Mixed commands carry their exceptions on the same executable registry row:
read-only `harness posture-plan` stays read-only, while `hook install` retains
the install sink and cannot inherit the generic governed lane.

Local verification admission is syntax-bounded too. The repo-local
`./node_modules/.bin/tsc --noEmit`, Compound console `scripts/*smoke.mjs`
commands through Bun or Node's TypeScript-strip mode, and read-only `xattr`
inspection remain automatic. Other JavaScript entrypoints, TypeScript emit
modes, and `xattr` write/delete/clear flags remain held. The same command
registry marks `profiles check` read-only and `profiles generate` governed.
Routine health faces follow the same split: `home doctor|where|verify`,
`architecture doctor`, and `contract hooks-authority` are reads; `home init`,
`runtime doctor` (which can opt into writeback), and contract
compile/import are governed. Backup, restore, compaction, runtime repair, and
unknown neighbors remain held.

A held action can use a durable, exact, one-use approval. The approval binds
operation, arguments, destination, source refs, tool schema and policy
revision, expires within fifteen minutes, and cannot override a deny or
security floor. Retrying does not change global permissions.

The normal-push standing grant is intentionally narrower than the generic
network-command lane. Only `git push origin <simple-source-ref>` classifies as
`shell.git_push_existing_remote` with destination `git-remote:existing`.
Omitted refspecs, alternate remotes, URL/path destinations, force/delete/mirror
or bulk flags, explicit refspec mappings, Git global flags, wrappers, and
composed commands remain generic network actions and hold. The capability still
requires an imported standing grant; classification alone never authorizes it.
The separate clean-`redact` publication gate remains mandatory.

## Supply-chain admission

Compound's existing source-manifest/canon owner now recognizes:

- `AGENTS.md`;
- `CLAUDE.md`;
- `SKILL.md`;
- plugin manifests;
- MCP manifests.

Each admitted item retains its exact body hash and a structural schema digest.
Value/content changes with the same structure remain automatic and receipted.
Added/removed schema fields, changed markdown control structure, changed
surface kind, missing attestation, or invalid syntax produce an admission hold
and do not advance the canon baseline. The drift still parks a no-promote
candidate, so the evidence is visible without becoming authority.

This is not a claim that native repo steering has been isolated: Codex and
Claude may read it before Compound. Universal action containment is therefore
still the authoritative defense.

## Recommended tools

| Tool | Decision | Role |
|---|---|---|
| Promptfoo `0.121.19` | Adopt | Primary harness-specific CI orchestration |
| AgentDojo commit-pinned suite | Adopt | Utility plus tool-agent injection benchmark |
| Garak `0.15.1` | Adopt | Broad probe on model/provider changes |
| PyRIT `1.0.0` | Adopt | Periodic adaptive/multi-turn campaigns |
| LlamaFirewall `1.0.3` | Shadow | Runtime scanner evidence only |
| Prompt Guard 2 22M/86M | Shadow | Cheap detector bakeoff; never an authority boundary |
| Snyk Agent Scan | Not admitted yet | Version, license, and data path were not verified |
| LLM Guard/Rebuff | Exclude | Archived/unmaintained choices |

Exact package, wheel, Git-tree, and model pins live in
`evals/prompt-injection/pins.json`. Detector results may only increase burden.

## Upgrade roadmap

### Now: structural containment

- Maintain universal generated Pre/PostToolUse coverage for Codex and Claude
  profiles.
- Keep live ingress/result lineage and exact action binding.
- Keep standing-grant automation and one-use retry approval.
- Gate source/tool schema drift through existing Compound owners.
- Run deterministic paired benign/attack traces on every change.

### Next: enroll every executor

- Route credential-bearing chat, unsandboxed app-server, connector gateway,
  account CLI, browser, GitHub, deploy, install, and provider CLI mutations
  through the same monitor.
- Remove direct credential-bearing subprocess paths or wrap them in scoped
  executors.
- Add native PreCompact and native child-result lifecycle capture where the
  harness supports it.
- Make installed-hook parity and exact permission invariants release gates.

### Then: reader/planner/executor split

```text
hostile source
  → quarantined reader (no secrets, writes, or general egress)
  → typed facts + citations
  → planner (operator goal + typed facts)
  → Compound ActionIntent/reference monitor
  → scoped executor with minimum credential/data
  → body-minimized receipt
```

Start with authenticated browser/email/GitHub flows, multimodal documents, and
package/install work because they combine hostile content with the highest
value accounts and credentials.

### Long term: secret/egress broker and continuous certification

- Keep credentials out of model context.
- Bind destination, method, recipient, DLP policy and minimum body at the
  broker.
- Use disposable synthetic accounts for adaptive red-team runs.
- Certify the exact model/prompt/hook/tool/MCP/policy/corpus revision matrix.
- Require zero S4/S5 success and at least 98% nightly benign automatic
  completion before release.

## Residual risk

The current change materially reduces the chance that a successful injection
becomes a host/account action, but it does not make hostile content safe to
mix. The largest remaining risks are:

1. credential-bearing chat and unsandboxed app-server CLI paths outside the
   universal monitor;
2. raw secrets appearing in model-visible tool output;
3. authenticated browser/email/GitHub work without an isolated reader/executor;
4. native compaction and child-event paths not yet captured before loss;
5. multimodal attacks and answer steering, which containment does not prevent;
6. registered schema coverage drifting from the live connector/tool catalog.

## References

- [OWASP LLM01:2025 Prompt Injection](https://genai.owasp.org/llmrisk/llm01-prompt-injection/)
- [NIST AI 600-1](https://doi.org/10.6028/NIST.AI.600-1)
- [NIST AI 100-2e2025](https://doi.org/10.6028/NIST.AI.100-2e2025)
- [MCP security best practices](https://modelcontextprotocol.io/docs/tutorials/security/security_best_practices)
- [Google secure-agent design](https://research.google/pubs/an-introduction-to-googles-approach-for-secure-ai-agents/)
- [CaMeL](https://arxiv.org/abs/2503.18813)
- [AgentDojo](https://arxiv.org/abs/2406.13352)
- [BIPIA](https://arxiv.org/abs/2312.14197)
- [InjecAgent](https://arxiv.org/abs/2403.02691)
