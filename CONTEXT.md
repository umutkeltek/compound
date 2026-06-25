# Compound Domain Glossary

This glossary defines product language. It is not an implementation spec.

## Core Terms

**Compound**
The local information kernel that turns evidence into current, contextual
knowledge.

**Local Monolith**
One installable binary/process boundary owns the information lifecycle. Compound
may use adapters for storage, models, editors, files, and retrieval engines, but
those adapters do not become independent authority services.

**Evidence**
Immutable, citable raw material. Evidence can support a claim, but it is never
truth by itself.

**Claim**
The smallest durable statement Compound can reason about. A claim can be
candidate, promoted, superseded, retired, or rejected.

**Position**
The current stance for an entity/topic, derived from claims. Current truth is a
position, not the newest note or the highest-ranked search hit.

**Placement**
Where information is canonically represented. Placement can be Compound-owned or
source-in-place, but each placed object needs one owner and one lookup path.

**Source-In-Place**
The original source remains in its natural home. Compound stores identifiers,
evidence references, claims, positions, and citations pointing to it. Use this
when copying the source would break ownership or privacy.

**Compound-Owned**
The object is canonical in Compound's ledger. This applies to claims, positions,
edges, scope assignments, and other derived knowledge objects whose lifecycle
Compound owns.

**Domain Adapter**
An edge module that imports or exports domain-specific information through
Compound's core object model. It may understand repo metadata, notes, issues,
decisions, or runtime facts. It must not redefine Evidence, Claim, Position,
visibility, or promotion semantics.

**Private Overlay**
User data, private adapters, credentials, personal doctrine, local paths, and
product-specific sources outside the public core. Public Compound must work with
synthetic fixtures without this overlay.

## Design Distinctions

**Monolith vs Microservice**
Compound is one product/runtime boundary with deep internal modules. Modules
define lifecycle invariants; they are not separate services.

**Findable vs Centralized**
Findable does not require every source file to move into one folder. Compound can
keep sources in place while centralizing identifiers, claims, positions,
citations, and context-pack assembly.
