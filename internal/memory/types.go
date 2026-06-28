// Package memory holds the OWNED object model: Evidence -> Claim -> Position ->
// Pattern, with Entity as the routing backbone and typed Edges for evolution.
// See docs/memory-architecture.md. These types are the spine; no storage engine
// is allowed to redefine them.
package memory

import "time"

type Visibility string

const (
	VisOperational Visibility = "operational" // ambient-injectable
	VisWorld       Visibility = "world"       // ambient-injectable
	VisPersonal    Visibility = "personal"    // gated
	VisPrivate     Visibility = "private"     // gated, never ambient
)

type Actor string

const (
	ActorUser      Actor = "user"
	ActorAssistant Actor = "assistant"
	ActorTool      Actor = "tool"
	ActorSystem    Actor = "system"
	ActorAgent     Actor = "agent"
)

// EvidenceItem is immutable, citable raw material. Never "truth" — only material.
type EvidenceItem struct {
	ID          string     `json:"id"`
	SourceKind  string     `json:"source_kind"` // e.g. claude_code, codex, agent_harness, repo, note
	SourceID    string     `json:"source_id"`
	SessionID   string     `json:"session_id,omitempty"`
	TurnID      string     `json:"turn_id,omitempty"`
	ArtifactURI string     `json:"artifact_uri,omitempty"`
	QuoteText   string     `json:"quote_text,omitempty"`
	ContentHash string     `json:"content_hash"`
	EventTime   *time.Time `json:"event_time,omitempty"` // when the discussed thing happened
	ObservedAt  time.Time  `json:"observed_at"`          // when compound ingested it
	Actor       Actor      `json:"actor"`
	Visibility  Visibility `json:"visibility"`
	Provenance  string     `json:"provenance"`
}

type EntityType string

const (
	EntProject  EntityType = "project"
	EntProduct  EntityType = "product"
	EntRepo     EntityType = "repo"
	EntServer   EntityType = "server"
	EntPerson   EntityType = "person"
	EntConcept  EntityType = "concept" // only when PROMOTED, not every embedding cluster
	EntDoctrine EntityType = "doctrine"
	EntTool     EntityType = "tool"
)

// Entity is the routing backbone — the thing scope resolves to.
type Entity struct {
	Slug              string     `json:"slug"`
	Type              EntityType `json:"type"`
	Aliases           []string   `json:"aliases,omitempty"`
	Identifiers       []string   `json:"identifiers,omitempty"` // repo URL, bundle id, domain, path prefix
	ParentSlug        string     `json:"parent_slug,omitempty"`
	VisibilityDefault Visibility `json:"visibility_default"`
	CreatedAt         time.Time  `json:"created_at"`
	RetiredAt         *time.Time `json:"retired_at,omitempty"`
}

type ClaimType string

const (
	ClaimPreference   ClaimType = "preference"
	ClaimDecision     ClaimType = "decision"
	ClaimArchitecture ClaimType = "architecture"
	ClaimTypeStatus   ClaimType = "status"
	ClaimConstraint   ClaimType = "constraint"
)

type ClaimStatus string

const (
	StatusCandidate  ClaimStatus = "candidate" // distiller output; NOT canon
	StatusPromoted   ClaimStatus = "promoted"
	StatusSuperseded ClaimStatus = "superseded"
	StatusRetired    ClaimStatus = "retired"
	StatusRejected   ClaimStatus = "rejected"
)

type PromotionBasis string

const (
	PromoExplicitConfirmation PromotionBasis = "explicit_confirmation"
	PromoCuratedPage          PromotionBasis = "curated_page"
	PromoDecisionRecord       PromotionBasis = "decision_record"
	PromoRuntimeProof         PromotionBasis = "runtime_proof"
)

// Claim is the smallest promotable durable memory. The distiller may emit
// candidates; it may NOT canonize them — promotion requires a PromotionBasis.
type Claim struct {
	ID             string         `json:"id"`
	SubjectEntity  string         `json:"subject_entity_slug"`
	Predicate      string         `json:"predicate"` // prefers, decided, rejected, uses, owns, deployed_on...
	Object         string         `json:"object"`
	ClaimType      ClaimType      `json:"claim_type"`
	Qualifiers     map[string]any `json:"qualifiers,omitempty"`
	EffectiveFrom  *time.Time     `json:"effective_from,omitempty"`
	EffectiveTo    *time.Time     `json:"effective_to,omitempty"`
	EventTime      *time.Time     `json:"event_time,omitempty"`
	Confidence     float64        `json:"confidence"`
	Visibility     Visibility     `json:"visibility"`
	Status         ClaimStatus    `json:"status"`
	EvidenceRefs   []string       `json:"evidence_refs,omitempty"`
	PromotionBasis PromotionBasis `json:"promotion_basis,omitempty"`
	CreatedBy      string         `json:"created_by"` // distiller, user, import, resolver
	ResolverVer    string         `json:"resolver_version,omitempty"`
}

// Topic returns the claim's topic slug (its domain qualifier, else "general").
// Positions and Patterns group claims by (SubjectEntity, Topic).
func (c Claim) Topic() string {
	if c.Qualifiers != nil {
		if d, ok := c.Qualifiers["domain"].(string); ok && d != "" {
			return d
		}
	}
	return "general"
}

// Position is the current durable stance on a topic that can evolve. Current
// truth is a Position, NOT "sort claims by date and pray".
type Position struct {
	ID            string    `json:"id"`
	EntitySlug    string    `json:"entity_slug"`
	TopicSlug     string    `json:"topic_slug"`
	ActiveClaimID string    `json:"active_claim_id"`
	Status        string    `json:"status"` // active | unstable | superseded | retired
	ClaimChain    []string  `json:"claim_chain,omitempty"`
	LastReviewed  time.Time `json:"last_reviewed_at"`
}

// Pattern is a promoted aggregate WITH support — not "the model found a theme".
// Not first-class canon in v1.
type Pattern struct {
	ID              string    `json:"id"`
	ScopeEntitySlug string    `json:"scope_entity_slug,omitempty"`
	Description     string    `json:"description"`
	SupportClaims   []string  `json:"supporting_claim_ids,omitempty"`
	SupportSessions []string  `json:"supporting_session_ids,omitempty"`
	NegativeRefs    []string  `json:"negative_evidence_refs,omitempty"`
	Confidence      float64   `json:"confidence"`
	Status          string    `json:"status"` // hypothesis | promoted | retired
	FirstSeen       time.Time `json:"first_seen_at"`
	LastSeen        time.Time `json:"last_seen_at"`
	LastVerified    time.Time `json:"last_verified_at"`
}

type EdgeType string

const (
	EdgeSupersedes  EdgeType = "supersedes"
	EdgeContradicts EdgeType = "contradicts"
	EdgeRefines     EdgeType = "refines"
	EdgeSupports    EdgeType = "supports"
	EdgeMentions    EdgeType = "mentions"
	EdgeOwns        EdgeType = "owns"
	EdgePartOf      EdgeType = "part_of"
)

// Edge is a relational graph edge — temporal evolution + traversal without a
// graph database.
type Edge struct {
	FromID        string     `json:"from_id"`
	FromType      string     `json:"from_type"`
	EdgeType      EdgeType   `json:"edge_type"`
	ToID          string     `json:"to_id"`
	ToType        string     `json:"to_type"`
	EffectiveFrom *time.Time `json:"effective_from,omitempty"`
	EffectiveTo   *time.Time `json:"effective_to,omitempty"`
	EvidenceRefs  []string   `json:"evidence_refs,omitempty"`
}
