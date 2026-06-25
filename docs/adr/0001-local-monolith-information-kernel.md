# ADR 0001: Compound Is A Local Monolith Information Kernel

Status: accepted

## Context

The reference system accumulated many useful surfaces, but operating cost grew
when authority, generated views, cwd assumptions, and data placement split across
many roots.

Compound should preserve the useful information lifecycle without reproducing
microservice-like operating tax.

## Decision

Compound is a local monolith at the product boundary. It exposes one CLI/binary
and one canonical ledger authority. Internally, it uses deep modules and
adapters. External storage engines, LLMs, and domain sources are rented adapters,
not independent authority services.

## Consequences

- Agents get one stable query path.
- Sources can stay in place while derived knowledge is findable through Compound.
- The append-only ledger remains the canonical store for derived knowledge
  objects.
- Domain extensibility happens through adapters and vocabularies, not new
  services.
- Public Compound must work with synthetic fixtures and no private overlay.

## Non-Goals

- Do not turn Compound into an agent orchestrator.
- Do not require all source files to move into one folder.
- Do not make one microservice per layer.
- Do not copy private memory or private control-plane surfaces into the public
  core.
