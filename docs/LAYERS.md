# Compound Layers

Compound is one local information kernel. Layers are concerns inside that kernel,
not services, repos, or separate authorities.

| # | Layer | Concern | Current Shape |
|---|---|---|---|
| 1 | Memory | What is known: evidence-backed claims, positions, patterns, context packs | Building |
| 2 | Authority | What is true about external things: repo, host, runtime, storage, domain | Early importer |
| 3 | Decisions | What has been committed to, with status and review triggers | Early importer |
| 4 | Procedures | How repeatable work is done | Planned |
| 5 | Permissions | What actors may do on behalf of a user/team | Planned |
| 6 | Doctrine | Durable principles and preferences | Planned |

## Rented Engines Are Not Layers

Storage engines, retrieval engines, LLM providers, editors, and external tools sit
behind adapters. They can improve recall or import/export, but they do not own the
object lifecycle.

```text
external source / engine
-> adapter
-> Evidence / Entity / Claim / Edge
-> promotion / reconciliation
-> Position / ContextPack
```

## Cross-Cutting Invariants

- Provenance: every durable object is citeable.
- Visibility: privacy and eligibility are filtered before recall.
- Time: claims can be superseded without destroying evidence.
- Placement: every object has one canonical lookup path.
- Cwd independence: current working directory is a hint at most, never authority.
- Subtraction: adding a layer should retire scattered handling, not create another
  parallel home.

## Build Discipline

Build one concern into the existing kernel, prove it with a working command and
synthetic fixtures, then retire the parallel handling it replaces. Do not begin a
new service because the information type feels new.
