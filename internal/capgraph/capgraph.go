// Package capgraph is the P8 Capability Graph: a typed graph of capability NODES (skills are one
// kind) connected by typed EDGES, with the evolution-feedback edges (missed_when /
// false_triggered_by / validated_by / retired_by) that let it LEARN which capability fits which
// context. It closes the loop with the distiller: a CapabilityObservation re-weights surfacing.
// Two invariants: AUTO-SURFACE, NEVER AUTO-EXECUTE — Surface returns relevant nodes for a task; it
// runs nothing (execution still needs WorkEnvelope + ActionIntent + PolicyDecision); and a missed/
// false trigger CHANGES future surfacing probability rather than silently re-tuning a prompt.
// State lives under COMPOUND_HOME. See docs/roadmap/phases.md (P8) and the contracts README.
package capgraph

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"compound/internal/candidates"
	"compound/internal/envelope"
	"compound/internal/eventstore"
)

// NodeKinds — skills are one kind, not the whole graph.
var NodeKinds = []string{"lens", "procedure", "script", "tool", "mcp", "model_profile", "harness", "eval", "workflow", "adapter", "doctrine", "taste_standard", "skill", "instruction_rule", "agent-lane"}

// Edge kinds: routing + the evolution feedback edges.
const (
	EdSurfacesWith     = "surfaces_with"
	EdRequires         = "requires"
	EdConflictsWith    = "conflicts_with"
	EdSpecializes      = "specializes"
	EdFallbackFor      = "fallback_for"
	EdValidatedBy      = "validated_by"
	EdMissedWhen       = "missed_when"
	EdFalseTriggeredBy = "false_triggered_by"
	EdRetiredBy        = "retired_by"
	EdEvaluatedBy      = "evaluated_by"
	EdCapableOf        = "capable_of"
	EdTendsTo          = "tends_to"
)

var EdgeKinds = []string{EdSurfacesWith, EdRequires, EdConflictsWith, EdSpecializes, EdFallbackFor, EdValidatedBy, EdMissedWhen, EdFalseTriggeredBy, EdRetiredBy, EdEvaluatedBy, EdCapableOf, EdTendsTo}

// FeedbackEdges are the ones a CapabilityObservation can write (context:<token> → node).
var FeedbackEdges = map[string]bool{EdMissedWhen: true, EdFalseTriggeredBy: true}

// InstructionRuleWhen describes one trigger dimension for a Tier-1 instruction rule.
type InstructionRuleWhen struct {
	Kind   string   `json:"kind" yaml:"kind"`
	Values []string `json:"values" yaml:"values"`
}

type InstructionRoute struct {
	Output      string `json:"output" yaml:"output"`
	Destination string `json:"destination" yaml:"destination"`
}

// Node is a capability. Triggers are the task tokens that surface it; Weight is its base surfacing
// strength (learned feedback adds/subtracts on top). InstructionRule nodes add Tier-1 rule
// metadata but remain offers: they read/route/boundary context, never execute.
type Node struct {
	ID           string                `json:"id"`
	Kind         string                `json:"kind"`
	Name         string                `json:"name"`
	Triggers     []string              `json:"triggers"`
	Weight       float64               `json:"weight"`
	Retired      bool                  `json:"retired,omitempty"`
	EvidenceOnly bool                  `json:"evidence_only,omitempty"`
	When         []InstructionRuleWhen `json:"when,omitempty"`
	Read         []string              `json:"read,omitempty"`
	Route        []InstructionRoute    `json:"route,omitempty"`
	Boundary     []string              `json:"boundary,omitempty"`
	Tier         int                   `json:"tier,omitempty"`
}

// Edge is a typed relation. For feedback edges From is "context:<token>" and To is a node id.
type Edge struct {
	Kind          string  `json:"kind"`
	From          string  `json:"from"`
	To            string  `json:"to"`
	Confidence    float64 `json:"confidence"`
	Weight        float64 `json:"weight,omitempty"`
	Provenance    string  `json:"provenance,omitempty"`
	SourceReceipt string  `json:"source_receipt,omitempty"`
	CreatedAt     string  `json:"created_at"`
}

type Store struct {
	home string

	semanticMu   sync.Mutex
	semanticLane string
}

type Stats struct {
	NodeCount      int            `json:"node_count"`
	EdgeCount      int            `json:"edge_count"`
	EdgeKindCounts map[string]int `json:"edge_kind_counts,omitempty"`
	SemanticLane   string         `json:"semantic_lane,omitempty"`
}

const SurfaceSchemaVersion = "compound.capability_surface.v0.1"

const (
	semanticLaneOff      = "off"
	semanticLaneOn       = "on"
	semanticLaneDegraded = "degraded"
	semanticFloor        = 0.55
	semanticWeight       = 2.0
	defaultEmbedTimeout  = 3 * time.Second
)

func Open(homePath string) (*Store, error) {
	if strings.TrimSpace(homePath) == "" {
		return nil, fmt.Errorf("no COMPOUND_HOME (run `compound home init`)")
	}
	if err := os.MkdirAll(filepath.Join(homePath, "registries"), 0o755); err != nil {
		return nil, err
	}
	return &Store{home: homePath}, nil
}

// OpenReadOnly opens an existing capability graph without creating runtime
// directories. Read-only front doors use it so an absent home stays absent.
func OpenReadOnly(homePath string) (*Store, error) {
	if strings.TrimSpace(homePath) == "" {
		return nil, fmt.Errorf("no COMPOUND_HOME (run `compound home init`)")
	}
	registries := filepath.Join(homePath, "registries")
	info, err := os.Stat(registries)
	if err != nil {
		return nil, fmt.Errorf("open capability graph read-only: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("open capability graph read-only: %s is not a directory", registries)
	}
	return &Store{home: homePath}, nil
}

func (s *Store) setSemanticLane(lane string) {
	if lane == "" {
		lane = semanticLaneOff
	}
	s.semanticMu.Lock()
	s.semanticLane = lane
	s.semanticMu.Unlock()
}

func (s *Store) getSemanticLane() string {
	s.semanticMu.Lock()
	defer s.semanticMu.Unlock()
	if s.semanticLane == "" {
		return semanticLaneOff
	}
	return s.semanticLane
}

func (s *Store) nodesLog() string {
	return filepath.Join(s.home, "registries", "capability-nodes.jsonl")
}
func (s *Store) edgesLog() string {
	return filepath.Join(s.home, "registries", "capability-edges.jsonl")
}

// Exists reports whether a home already has capability graph node state. It is
// used by read-only consumers so orientation does not create runtime dirs.
func Exists(homePath string) bool {
	if strings.TrimSpace(homePath) == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(homePath, "registries", "capability-nodes.jsonl"))
	return err == nil
}

// Add upserts a node (append-only; last write wins on load).
func (s *Store) Add(n Node) error {
	if !contains(NodeKinds, n.Kind) {
		return fmt.Errorf("node kind must be one of %v, got %q", NodeKinds, n.Kind)
	}
	if n.Weight == 0 {
		n.Weight = 1.0
	}
	nodes, err := s.loadNodes()
	if err != nil {
		return err
	}
	for _, existing := range nodes {
		if existing.ID == n.ID && reflect.DeepEqual(existing, n) {
			return nil
		}
	}
	return appendJSONL(s.nodesLog(), &n)
}

// AddEdge appends a typed graph edge. It is for deterministic importers such as
// tool topology; feedback learning still goes through Observe.
func (s *Store) AddEdge(e Edge, now time.Time) error {
	if !contains(EdgeKinds, e.Kind) {
		return fmt.Errorf("edge kind must be one of %v, got %q", EdgeKinds, e.Kind)
	}
	if strings.TrimSpace(e.From) == "" || strings.TrimSpace(e.To) == "" {
		return fmt.Errorf("edge needs from and to")
	}
	if e.Confidence == 0 {
		e.Confidence = 1.0
	}
	if e.Confidence < 0 {
		return fmt.Errorf("edge confidence must be >= 0, got %.2f", e.Confidence)
	}
	if strings.TrimSpace(e.CreatedAt) == "" {
		e.CreatedAt = now.UTC().Format(time.RFC3339)
	}
	return appendJSONL(s.edgesLog(), &e)
}

// Observe records evolution-feedback edges from a capability observation: missed_when (the node
// was relevant to a context but not surfaced) raises its future surfacing for that context;
// false_triggered_by lowers it. A multi-word context is tokenized into ONE edge per token (so a
// phrase context can match a task the same way Surface tokenizes), and confidence must be > 0 (a
// zero/negative edge is inert or sign-inverted — refused loudly). This is what makes the graph LEARN.
func (s *Store) Observe(node, signal, context string, conf float64, now time.Time) ([]Edge, error) {
	if !FeedbackEdges[signal] {
		return nil, fmt.Errorf("observe signal must be missed_when or false_triggered_by, got %q", signal)
	}
	if strings.TrimSpace(node) == "" {
		return nil, fmt.Errorf("observe needs a node")
	}
	if conf <= 0 {
		return nil, fmt.Errorf("observe confidence must be > 0, got %.2f", conf)
	}
	toks := tokenList(context)
	if len(toks) == 0 {
		return nil, fmt.Errorf("observe context %q has no usable tokens", context)
	}
	var edges []Edge
	for _, tok := range toks {
		e := Edge{Kind: signal, From: "context:" + tok, To: node, Confidence: conf, CreatedAt: now.UTC().Format(time.RFC3339)}
		if err := appendJSONL(s.edgesLog(), &e); err != nil {
			return edges, err
		}
		edges = append(edges, e)
	}
	return edges, nil
}

// Surfaced is a node the graph offers for a task, with the score that ranked it. It is OFFERED,
// not run.
type Surfaced struct {
	Node          Node                 `json:"node"`
	Score         float64              `json:"score"`
	Why           string               `json:"why"`
	MatchedTokens []string             `json:"matched_tokens,omitempty"`
	Constraints   SurfacingConstraints `json:"constraints,omitempty"`
}

type SurfaceReport struct {
	SchemaVersion      string                `json:"schema_version"`
	AuthorityRef       string                `json:"authority_ref"`
	AuthorityRevision  string                `json:"authority_revision,omitempty"`
	Status             string                `json:"status"`
	Task               string                `json:"task"`
	Harness            string                `json:"harness,omitempty"`
	NodeCount          int                   `json:"node_count"`
	EdgeCount          int                   `json:"edge_count"`
	SourceRecordCount  int                   `json:"source_record_count"`
	OfferCount         int                   `json:"offer_count"`
	OfferLimit         int                   `json:"offer_limit,omitempty"`
	OmittedOfferCount  int                   `json:"omitted_offer_count,omitempty"`
	Offers             []Surfaced            `json:"offers"`
	OfferSummaries     []SurfaceOfferSummary `json:"offer_summaries"`
	MatchedTokenCounts []MatchedTokenCount   `json:"matched_token_counts,omitempty"`
	MatchedEvidence    []SurfacingConstraint `json:"matched_evidence,omitempty"`
}

type SurfaceOfferSummary struct {
	ID            string   `json:"id"`
	Kind          string   `json:"kind"`
	Name          string   `json:"name"`
	Score         float64  `json:"score"`
	Why           string   `json:"why"`
	MatchedTokens []string `json:"matched_tokens,omitempty"`
	RequiredRefs  []string `json:"required_refs,omitempty"`
	ConflictRefs  []string `json:"conflict_refs,omitempty"`
	HarnessRefs   []string `json:"harness_refs,omitempty"`
	EvidenceRefs  []string `json:"evidence_refs,omitempty"`
	CostRiskNote  string   `json:"cost_risk_note,omitempty"`
}

type MatchedTokenCount struct {
	Token string `json:"token"`
	Count int    `json:"count"`
}

func NewSurfaceReport(task, harness string, offers []Surfaced, stats Stats, sourceRecordCount int) SurfaceReport {
	summaries := make([]SurfaceOfferSummary, 0, len(offers))
	for _, offer := range offers {
		summaries = append(summaries, SurfaceOfferSummary{
			ID:            offer.Node.ID,
			Kind:          offer.Node.Kind,
			Name:          offer.Node.Name,
			Score:         offer.Score,
			Why:           offer.Why,
			MatchedTokens: append([]string{}, offer.MatchedTokens...),
			RequiredRefs:  constraintRefs(offer.Constraints.Required),
			ConflictRefs:  constraintRefs(offer.Constraints.Conflicts),
			HarnessRefs:   constraintRefs(offer.Constraints.Harnesses),
			EvidenceRefs:  constraintRefs(offer.Constraints.Evidence),
			CostRiskNote:  offer.Constraints.CostRiskNote,
		})
	}
	return SurfaceReport{
		SchemaVersion:      SurfaceSchemaVersion,
		AuthorityRef:       CapabilityGraphAuthorityRef,
		Status:             "ok",
		Task:               strings.TrimSpace(task),
		Harness:            strings.TrimSpace(harness),
		NodeCount:          stats.NodeCount,
		EdgeCount:          stats.EdgeCount,
		SourceRecordCount:  sourceRecordCount,
		OfferCount:         len(offers),
		Offers:             offers,
		OfferSummaries:     summaries,
		MatchedTokenCounts: matchedTokenCounts(offers),
	}
}

// LimitOffers returns a read-model copy bounded to top-ranked offers while
// preserving OfferCount as the full graph result count.
func (r SurfaceReport) LimitOffers(limit int) SurfaceReport {
	if limit < 0 {
		return r
	}
	r.OfferLimit = limit
	if len(r.Offers) <= limit {
		return r
	}
	r.OmittedOfferCount = len(r.Offers) - limit
	r.Offers = append([]Surfaced{}, r.Offers[:limit]...)
	if len(r.OfferSummaries) > limit {
		r.OfferSummaries = append([]SurfaceOfferSummary{}, r.OfferSummaries[:limit]...)
	}
	return r
}

func matchedTokenCounts(offers []Surfaced) []MatchedTokenCount {
	counts := map[string]int{}
	for _, offer := range offers {
		for _, token := range offer.MatchedTokens {
			token = strings.TrimSpace(token)
			if token == "" {
				continue
			}
			counts[token]++
		}
	}
	out := make([]MatchedTokenCount, 0, len(counts))
	for token, count := range counts {
		out = append(out, MatchedTokenCount{Token: token, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Token < out[j].Token
	})
	return out
}

func constraintRefs(values []SurfacingConstraint) []string {
	refs := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		ref := strings.TrimSpace(value.Ref)
		if ref == "" || seen[ref] {
			continue
		}
		refs = append(refs, ref)
		seen[ref] = true
	}
	return refs
}

type SurfacingConstraint struct {
	Kind       string  `json:"kind"`
	Ref        string  `json:"ref"`
	Detail     string  `json:"detail,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

type SurfacingConstraints struct {
	Required              []SurfacingConstraint `json:"required,omitempty"`
	Conflicts             []SurfacingConstraint `json:"conflicts,omitempty"`
	Harnesses             []SurfacingConstraint `json:"harnesses,omitempty"`
	KnownMissedWhen       []SurfacingConstraint `json:"known_missed_when,omitempty"`
	KnownFalseTriggeredBy []SurfacingConstraint `json:"known_false_triggered_by,omitempty"`
	Evidence              []SurfacingConstraint `json:"evidence,omitempty"`
	CostRiskNote          string                `json:"cost_risk_note,omitempty"`
}

// Surface returns the capability nodes relevant to a task, ranked — and runs NOTHING. Base score
// is trigger overlap × weight; a missed_when edge for a present context token ADDS to the score
// (learned surfacing); a false_triggered_by edge SUBTRACTS. Retired nodes never surface.
func (s *Store) Surface(task string) ([]Surfaced, error) {
	return s.SurfaceForContext(SurfaceContext{Task: task})
}

// SurfaceForHarness returns task-relevant offers allowed for a harness. Nodes
// without harness edges are generic; nodes with surfaces_with -> harness:* edges
// surface only for listed harnesses.
func (s *Store) SurfaceForHarness(task, harness string) ([]Surfaced, error) {
	return s.SurfaceForContext(SurfaceContext{Task: task, Harness: harness})
}

// SurfaceContext is the scoped trigger input for capability and instruction-rule surfacing.
type SurfaceContext struct {
	Task         string
	RepoKind     string
	Harness      string
	FilePath     string
	Phase        string
	Embedder     NodeEmbedder
	EmbedTimeout time.Duration
}

// SurfaceForContext returns task-relevant offers for the full orientation context.
func (s *Store) SurfaceForContext(ctx SurfaceContext) ([]Surfaced, error) {
	return s.surface(ctx.Task, harnessID(ctx.Harness), ctx)
}

func (s *Store) surface(task, harness string, ctx SurfaceContext) ([]Surfaced, error) {
	nodes, err := s.loadNodes()
	if err != nil {
		return nil, err
	}
	edges, err := s.loadEdges()
	if err != nil {
		return nil, err
	}
	feedbackTokens := rawContextTokenSet(task)
	tokens := tokenSet(task)
	instructionTokens := instructionRuleTokenSet(ctx)
	pruneLegacyAdaptationMetaTokens(tokens, task)
	semanticLane := semanticLaneOff
	semantic := map[string]float64{}
	if ctx.Embedder != nil {
		timeout := ctx.EmbedTimeout
		if timeout <= 0 {
			timeout = defaultEmbedTimeout
		}
		semanticCtx, cancel := context.WithTimeout(context.Background(), timeout)
		scores, err := semanticScores(semanticCtx, ctx.Embedder, s.home, nodes, task)
		cancel()
		if err != nil {
			semanticLane = semanticLaneDegraded
		} else {
			semanticLane = semanticLaneOn
			semantic = scores
		}
	}
	s.setSemanticLane(semanticLane)

	// learned context → node adjustments
	validatedAdd := map[string]float64{}
	missedAdd := map[string]float64{}
	sub := map[string]float64{}
	harnesses := map[string]map[string]bool{}
	evidenceBonus, evidenceRefs := evidenceMatchedOwnerBonuses(nodes, edges, tokens)
	for _, e := range edges {
		if e.Kind == EdSurfacesWith && strings.HasPrefix(e.To, "harness:") {
			if harnesses[e.From] == nil {
				harnesses[e.From] = map[string]bool{}
			}
			harnesses[e.From][e.To] = true
			continue
		}
		ctx := strings.TrimPrefix(e.From, "context:")
		if e.From == ctx { // not a context edge
			continue
		}
		switch e.Kind {
		case EdValidatedBy:
			if !contextTokenMatches(tokens, ctx) {
				continue
			}
			validatedAdd[e.To] += edgeScore(e)
		case EdMissedWhen:
			if !contextTokenMatches(tokens, ctx) {
				continue
			}
			missedAdd[e.To] += edgeScore(e)
		case EdFalseTriggeredBy:
			if !contextTokenMatches(feedbackTokens, ctx) {
				continue
			}
			sub[e.To] += edgeScore(e)
		}
	}

	var out []Surfaced
	for _, n := range nodes {
		if n.Retired || n.EvidenceOnly {
			continue
		}
		if harness != "" && len(harnesses[n.ID]) > 0 && !harnesses[n.ID][harness] {
			continue
		}
		sem := semantic[n.ID]
		semanticMatched := sem >= semanticFloor
		matchTokens := tokens
		if n.Kind == "instruction_rule" {
			if !instructionRuleMatches(n, ctx, tokens, instructionTokens) && !semanticMatched {
				continue
			}
			matchTokens = instructionTokens
		}
		base, matchedTokens := surfaceTriggerScore(matchTokens, n)
		if n.Kind == "instruction_rule" && base == 0 {
			base = 1
		}
		if evidenceBonus[n.ID] > 0 {
			matchedTokens = appendMatchedTokens(matchedTokens, "evidence")
		}
		semanticBonus := 0.0
		if semanticMatched {
			semanticBonus = semanticWeight * (sem - semanticFloor) / (1 - semanticFloor) * n.Weight
		}
		score := base*n.Weight + semanticBonus + validatedAdd[n.ID] + missedAdd[n.ID] + evidenceBonus[n.ID] - sub[n.ID]
		if score <= 0 {
			continue
		}
		why := fmt.Sprintf("trigger×weight=%.1f", base*n.Weight)
		if semanticBonus > 0 {
			why += fmt.Sprintf(" +semantic=%.2f", sem)
		}
		if validatedAdd[n.ID] > 0 {
			why += fmt.Sprintf(" +validated_by=%.1f", validatedAdd[n.ID])
		}
		if missedAdd[n.ID] > 0 {
			why += fmt.Sprintf(" +missed_when=%.1f", missedAdd[n.ID])
		}
		if evidenceBonus[n.ID] > 0 {
			why += fmt.Sprintf(" +evidence=%.1f", evidenceBonus[n.ID])
		}
		if sub[n.ID] > 0 {
			why += fmt.Sprintf(" -false_triggered=%.1f", sub[n.ID])
		}
		constraints := capabilityConstraints(n.ID, edges)
		addReferenceBearingConstraints(&constraints, n)
		if n.Kind == "instruction_rule" {
			addInstructionRuleConstraints(&constraints, n)
		}
		for _, ref := range evidenceRefs[n.ID] {
			constraints.Evidence = append(constraints.Evidence, SurfacingConstraint{
				Kind:       "matched_evidence",
				Ref:        ref,
				Detail:     "matched task tokens through evidence-only capability evaluation",
				Confidence: evidenceBonus[n.ID],
			})
		}
		sortConstraints(constraints.Evidence)
		out = append(out, Surfaced{
			Node:          n,
			Score:         score,
			Why:           why,
			MatchedTokens: matchedTokens,
			Constraints:   constraints,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Node.ID < out[j].Node.ID
	})
	return out, nil
}

func evidenceMatchedOwnerBonuses(nodes []Node, edges []Edge, taskTokens map[string]bool) (map[string]float64, map[string][]string) {
	evalScore := map[string]float64{}
	for _, node := range nodes {
		if node.Retired || !node.EvidenceOnly {
			continue
		}
		base, _ := surfaceTriggerScore(taskTokens, node)
		if base <= 0 {
			continue
		}
		weight := node.Weight
		if weight < 0.5 {
			weight = 0.5
		}
		evalScore[node.ID] = base * weight
	}
	if len(evalScore) == 0 {
		return nil, nil
	}
	ownerBonus := map[string]float64{}
	ownerRefs := map[string][]string{}
	for _, edge := range edges {
		if edge.Kind != EdEvaluatedBy && edge.Kind != EdValidatedBy {
			continue
		}
		matchedEvalScore := evalScore[edge.To]
		if matchedEvalScore <= 0 {
			continue
		}
		conf := edge.Confidence
		if conf == 0 {
			conf = 1
		}
		bonus := matchedEvalScore * conf
		if edge.Kind == EdEvaluatedBy {
			bonus *= 0.75
		}
		ownerBonus[edge.From] += bonus
		ownerRefs[edge.From] = appendUniqueString(ownerRefs[edge.From], edge.To)
	}
	for owner, refs := range ownerRefs {
		sort.Strings(refs)
		ownerRefs[owner] = refs
	}
	return ownerBonus, ownerRefs
}

func addReferenceBearingConstraints(c *SurfacingConstraints, n Node) {
	readDetail := "source drilldown requested by matched capability"
	if n.Kind == "instruction_rule" {
		readDetail = "context ref requested by matched InstructionRule"
	}
	for _, ref := range n.Read {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		c.Required = append(c.Required, SurfacingConstraint{Kind: "read", Ref: ref, Detail: readDetail, Confidence: 1})
	}
	for _, boundary := range n.Boundary {
		boundary = strings.TrimSpace(boundary)
		if boundary == "" {
			continue
		}
		c.Conflicts = append(c.Conflicts, SurfacingConstraint{Kind: "boundary", Ref: instructionBoundaryRef(boundary), Detail: boundary, Confidence: 1})
	}
	sortConstraints(c.Required)
	sortConstraints(c.Conflicts)
}

func addInstructionRuleConstraints(c *SurfacingConstraints, n Node) {
	for _, route := range n.Route {
		output := strings.TrimSpace(route.Output)
		destination := strings.TrimSpace(route.Destination)
		if output == "" || destination == "" {
			continue
		}
		c.Required = append(c.Required, SurfacingConstraint{Kind: "route", Ref: output, Detail: destination, Confidence: 1})
	}
	if n.Tier > 0 {
		c.Evidence = append(c.Evidence, SurfacingConstraint{Kind: "tier", Ref: fmt.Sprintf("instruction-tier:%d", n.Tier), Detail: "InstructionRule tier", Confidence: 1})
	}
	c.CostRiskNote = "instruction offer only; constrains read refs, output routing, and boundaries; never auto-executes"
	sortConstraints(c.Required)
	sortConstraints(c.Conflicts)
	sortConstraints(c.Evidence)
}

func instructionBoundaryRef(boundary string) string {
	return "boundary://" + strings.Join(tokenList(boundary), "-")
}

func capabilityConstraints(nodeID string, edges []Edge) SurfacingConstraints {
	var c SurfacingConstraints
	c.CostRiskNote = "offer only; execution still requires WorkEnvelope, ActionIntent, and PolicyDecision"
	add := func(dst *[]SurfacingConstraint, kind, ref, detail string, conf float64) {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return
		}
		*dst = append(*dst, SurfacingConstraint{Kind: kind, Ref: ref, Detail: detail, Confidence: conf})
	}
	for _, e := range edges {
		switch e.Kind {
		case EdRequires:
			if e.From == nodeID {
				add(&c.Required, e.Kind, e.To, "", e.Confidence)
			}
		case EdConflictsWith:
			if e.From == nodeID {
				add(&c.Conflicts, e.Kind, e.To, "", e.Confidence)
			} else if e.To == nodeID {
				add(&c.Conflicts, e.Kind, e.From, "", e.Confidence)
			}
		case EdSurfacesWith:
			if e.From == nodeID && strings.HasPrefix(e.To, "harness:") {
				add(&c.Harnesses, e.Kind, e.To, "compatible harness", e.Confidence)
			}
		case EdMissedWhen:
			if e.To == nodeID && strings.HasPrefix(e.From, "context:") {
				add(&c.KnownMissedWhen, e.Kind, e.From, strings.TrimPrefix(e.From, "context:"), e.Confidence)
			}
		case EdFalseTriggeredBy:
			if e.To == nodeID && strings.HasPrefix(e.From, "context:") {
				add(&c.KnownFalseTriggeredBy, e.Kind, e.From, strings.TrimPrefix(e.From, "context:"), e.Confidence)
			}
		case EdValidatedBy, EdEvaluatedBy:
			if e.From == nodeID {
				add(&c.Evidence, e.Kind, e.To, "", e.Confidence)
			}
		}
	}
	sortConstraints(c.Required)
	sortConstraints(c.Conflicts)
	sortConstraints(c.Harnesses)
	sortConstraints(c.KnownMissedWhen)
	sortConstraints(c.KnownFalseTriggeredBy)
	sortConstraints(c.Evidence)
	return c
}

func harnessID(harness string) string {
	harness = strings.TrimSpace(strings.ToLower(harness))
	if harness == "" || strings.HasPrefix(harness, "harness:") {
		return harness
	}
	return "harness:" + harness
}

// Consume reads the distiller's operationalized CapabilityObservations from the inbox and applies
// them as feedback edges (the distiller→graph bridge). Convention: subject=node id, detail=signal
// (missed_when|false_triggered_by), statement=context token. Returns how many were applied.
func (s *Store) Consume(homePath string, now time.Time) (int, error) {
	dir := filepath.Join(homePath, candidates.InboxDir(candidates.CapabilityObs)) // same canonical dir the distiller writes
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	applied := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var obs struct {
			Subject    string  `json:"subject"`
			Statement  string  `json:"statement"`
			Detail     string  `json:"detail"`
			Verdict    string  `json:"verdict"`
			Confidence float64 `json:"confidence"`
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return applied, err
		}
		// Respect adjudication: only OPERATIONALIZED feedback observations apply (a parked / low-
		// confidence one must not silently re-weight surfacing — that would bypass the floor).
		if json.Unmarshal(b, &obs) != nil || obs.Verdict != "operationalize" ||
			!FeedbackEdges[obs.Detail] || obs.Subject == "" || obs.Statement == "" || obs.Confidence <= 0 {
			continue
		}
		if _, err := s.Observe(obs.Subject, obs.Detail, obs.Statement, obs.Confidence, now); err != nil {
			return applied, err
		}
		applied++
	}
	return applied, nil
}

// ReceiptConsumeReport summarizes how runtime receipts affected the graph.
type ReceiptConsumeReport struct {
	ReceiptsScanned        int      `json:"receipts_scanned"`
	UsedCapabilitiesSeen   int      `json:"used_capabilities_seen"`
	MissedCapabilitiesSeen int      `json:"missed_capabilities_seen"`
	FalseTriggersSeen      int      `json:"false_triggers_seen"`
	AppliedEdges           int      `json:"applied_edges"`
	AppliedValidatedEdges  int      `json:"applied_validated_edges"`
	AppliedMissedEdges     int      `json:"applied_missed_edges"`
	AppliedFalseEdges      int      `json:"applied_false_edges"`
	SkippedNonCompleted    int      `json:"skipped_non_completed"`
	SkippedUnchanged       int      `json:"skipped_unchanged"`
	SkippedMissingContext  int      `json:"skipped_missing_context"`
	SkippedInvalidReceipt  int      `json:"skipped_invalid_receipt"`
	SkippedUnknown         []string `json:"skipped_unknown"`
}

// ConsumeReceipts turns receipt feedback into graph edges. Used capabilities
// validate only when a completed receipt changed state. Explicit missed
// capabilities add missed_when context edges from the receipt summary; explicit
// false-triggered capabilities add false_triggered_by edges from the same
// summary. Failed receipts can still teach misses/false triggers.
// ContextPack offers in skills.candidate are
// intentionally ignored: offer is not use.
func (s *Store) ConsumeReceipts(homePath string, now time.Time) (ReceiptConsumeReport, error) {
	var report ReceiptConsumeReport
	events, err := eventstore.Open(homePath)
	if err != nil {
		return report, err
	}
	entries, err := events.Scan(eventstore.ScanOptions{})
	if err != nil {
		if os.IsNotExist(err) {
			return report, nil
		}
		return report, err
	}

	nodes, err := s.loadNodes()
	if err != nil {
		return report, err
	}
	nodeIDs := map[string]bool{}
	for _, n := range nodes {
		if !n.Retired {
			nodeIDs[n.ID] = true
		}
	}

	edges, err := s.loadEdges()
	if err != nil {
		return report, err
	}
	existingEdges := map[string]bool{}
	for _, e := range edges {
		existingEdges[edgeKey(e)] = true
	}
	unknown := map[string]bool{}

	for _, entry := range entries {
		record, err := entry.Read()
		if err != nil {
			return report, err
		}
		data := record.Bytes
		var head struct {
			SchemaVersion string `json:"schema_version"`
		}
		if err := json.Unmarshal(data, &head); err != nil {
			return report, fmt.Errorf("parse event envelope %s: %w", entry.Name, err)
		}
		if head.SchemaVersion != envelope.SVReceipt {
			continue
		}

		var rec envelope.Receipt
		if err := json.Unmarshal(data, &rec); err != nil {
			return report, fmt.Errorf("parse receipt %s: %w", entry.Name, err)
		}
		if err := rec.Validate(); err != nil {
			report.SkippedInvalidReceipt++
			continue
		}
		report.ReceiptsScanned++

		changed := rec.EffectiveChanged()
		canValidateUse := rec.Outcome == "completed" && changed
		if rec.Outcome != "completed" {
			report.SkippedNonCompleted++
		}
		if !changed {
			report.SkippedUnchanged++
		}
		contextTokens := tokenList(rec.StateDelta.Summary)
		provenance, weight := receiptEdgeProvenance(rec)

		if canValidateUse {
			for _, raw := range rec.Skills.Used {
				nodeID := capabilityNodeID(raw)
				if nodeID == "" {
					continue
				}
				report.UsedCapabilitiesSeen++
				if !nodeIDs[nodeID] {
					if !unknown[nodeID] {
						report.SkippedUnknown = append(report.SkippedUnknown, nodeID)
						unknown[nodeID] = true
					}
					continue
				}
				edge := receiptEdge(EdValidatedBy, nodeID, rec.ID, rec.ID, provenance, weight, now)
				if added, err := s.addEdgeOnce(edge, existingEdges, now); err != nil {
					return report, err
				} else if added {
					report.AppliedEdges++
					report.AppliedValidatedEdges++
				}
				for _, tok := range contextTokens {
					contextEdge := receiptEdge(EdValidatedBy, "context:"+tok, nodeID, rec.ID, provenance, weight, now)
					if added, err := s.addEdgeOnce(contextEdge, existingEdges, now); err != nil {
						return report, err
					} else if added {
						report.AppliedEdges++
						report.AppliedValidatedEdges++
					}
				}
			}
		}

		for _, raw := range rec.Skills.Missed {
			nodeID := capabilityNodeID(raw)
			if nodeID == "" {
				continue
			}
			report.MissedCapabilitiesSeen++
			if !nodeIDs[nodeID] {
				if !unknown[nodeID] {
					report.SkippedUnknown = append(report.SkippedUnknown, nodeID)
					unknown[nodeID] = true
				}
				continue
			}
			if len(contextTokens) == 0 {
				report.SkippedMissingContext++
				continue
			}
			for _, tok := range contextTokens {
				edge := receiptEdge(EdMissedWhen, "context:"+tok, nodeID, rec.ID, provenance, weight, now)
				if added, err := s.addEdgeOnce(edge, existingEdges, now); err != nil {
					return report, err
				} else if added {
					report.AppliedEdges++
					report.AppliedMissedEdges++
				}
			}
		}

		for _, raw := range rec.Skills.FalseTriggered {
			nodeID := capabilityNodeID(raw)
			if nodeID == "" {
				continue
			}
			report.FalseTriggersSeen++
			if !nodeIDs[nodeID] {
				if !unknown[nodeID] {
					report.SkippedUnknown = append(report.SkippedUnknown, nodeID)
					unknown[nodeID] = true
				}
				continue
			}
			if len(contextTokens) == 0 {
				report.SkippedMissingContext++
				continue
			}
			for _, tok := range contextTokens {
				edge := receiptEdge(EdFalseTriggeredBy, "context:"+tok, nodeID, rec.ID, provenance, weight, now)
				if added, err := s.addEdgeOnce(edge, existingEdges, now); err != nil {
					return report, err
				} else if added {
					report.AppliedEdges++
					report.AppliedFalseEdges++
				}
			}
		}
	}
	sort.Strings(report.SkippedUnknown)
	return report, nil
}

// ConsumeReceiptsIfGraphExists is the automatic feedback hook used by closure
// clients. It avoids creating graph state just because work closed.
func ConsumeReceiptsIfGraphExists(homePath string, now time.Time) (ReceiptConsumeReport, bool, error) {
	if !Exists(homePath) {
		return ReceiptConsumeReport{}, false, nil
	}
	store, err := Open(homePath)
	if err != nil {
		return ReceiptConsumeReport{}, true, err
	}
	report, err := store.ConsumeReceipts(homePath, now)
	return report, true, err
}

func (s *Store) addEdgeOnce(edge Edge, existing map[string]bool, now time.Time) (bool, error) {
	key := edgeKey(edge)
	if existing[key] {
		return false, nil
	}
	if err := s.AddEdge(edge, now); err != nil {
		return false, err
	}
	existing[key] = true
	return true, nil
}

func (s *Store) List() ([]Node, error) { return s.loadNodes() }

func (s *Store) Edges() ([]Edge, error) { return s.loadEdges() }

func (s *Store) Stats() (Stats, error) {
	nodes, err := s.loadNodes()
	if err != nil {
		return Stats{}, err
	}
	edges, err := s.loadEdges()
	if err != nil {
		return Stats{}, err
	}
	edgeKindCounts := map[string]int{}
	for _, edge := range edges {
		edgeKindCounts[edge.Kind]++
	}
	return Stats{NodeCount: len(nodes), EdgeCount: len(edges), EdgeKindCounts: edgeKindCounts, SemanticLane: s.getSemanticLane()}, nil
}

// --- load (append-only, last-wins by id) ---

func (s *Store) loadNodes() ([]Node, error) {
	byID := map[string]Node{}
	var order []string
	err := readJSONL(s.nodesLog(), func(line []byte) error {
		var n Node
		if err := json.Unmarshal(line, &n); err != nil {
			return nil // skip a torn/malformed line rather than bricking the whole graph
		}
		if _, seen := byID[n.ID]; !seen {
			order = append(order, n.ID)
		}
		byID[n.ID] = n
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]Node, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out, nil
}

// loadEdges dedups last-wins by edgeKey, so re-Consuming the same observation is
// IDEMPOTENT (it does not double-count a feedback edge); distinct contexts or receipts still accumulate.
func (s *Store) loadEdges() ([]Edge, error) {
	byKey := map[string]Edge{}
	var order []string
	err := readJSONL(s.edgesLog(), func(line []byte) error {
		var e Edge
		if err := json.Unmarshal(line, &e); err != nil {
			return nil // skip a torn/malformed line
		}
		k := edgeKey(e)
		if _, seen := byKey[k]; !seen {
			order = append(order, k)
		}
		byKey[k] = e
		return nil
	})
	out := make([]Edge, 0, len(order))
	for _, k := range order {
		out = append(out, byKey[k])
	}
	return out, err
}

// --- helpers ---

func instructionRuleTokenSet(ctx SurfaceContext) map[string]bool {
	return tokenSet(strings.Join([]string{ctx.Task, ctx.RepoKind, ctx.Harness, ctx.FilePath, ctx.Phase}, " "))
}

func instructionRuleMatches(n Node, ctx SurfaceContext, taskTokens, contextTokens map[string]bool) bool {
	if n.Kind != "instruction_rule" {
		return true
	}
	if len(n.When) == 0 {
		return true
	}
	for _, when := range n.When {
		if !instructionRuleWhenMatches(when, ctx, taskTokens, contextTokens) {
			return false
		}
	}
	return true
}

func instructionRuleWhenMatches(when InstructionRuleWhen, ctx SurfaceContext, taskTokens, contextTokens map[string]bool) bool {
	kind := strings.TrimSpace(strings.ToLower(when.Kind))
	if kind == "" {
		kind = "task-class"
	}
	for _, value := range when.Values {
		value = strings.TrimSpace(strings.ToLower(value))
		if value == "" {
			continue
		}
		switch kind {
		case "task-class":
			if contextValueMatches(taskTokens, value) {
				return true
			}
		case "repo-kind":
			if normalizeTriggerValue(ctx.RepoKind) == normalizeTriggerValue(value) {
				return true
			}
		case "harness":
			if normalizeTriggerValue(ctx.Harness) == normalizeTriggerValue(value) {
				return true
			}
		case "file-path":
			if strings.Contains(strings.ToLower(ctx.FilePath), value) || contextValueMatches(contextTokens, value) {
				return true
			}
		case "phase":
			if normalizeTriggerValue(ctx.Phase) == normalizeTriggerValue(value) {
				return true
			}
		}
	}
	return false
}

func contextValueMatches(tokens map[string]bool, value string) bool {
	for _, token := range tokenList(value) {
		if contextTokenMatches(tokens, token) {
			return true
		}
	}
	return false
}

func normalizeTriggerValue(value string) string {
	return strings.ReplaceAll(strings.TrimSpace(strings.ToLower(value)), "_", "-")
}

func tokenSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, t := range tokenList(s) {
		out[t] = true
	}
	if out["go"] && hasAnyToken(out, goEngineeringContextTokens) {
		out["golang"] = true
		out[goLanguageAliasToken] = true
	}
	return out
}

func contextTokenMatches(tokens map[string]bool, ctx string) bool {
	ctx = strings.TrimSpace(strings.ToLower(ctx))
	if ctx == "" {
		return false
	}
	if tokens[ctx] {
		return true
	}
	if strings.HasSuffix(ctx, "y") {
		if tokens[strings.TrimSuffix(ctx, "y")+"ies"] {
			return true
		}
	}
	if strings.HasSuffix(ctx, "s") {
		return tokens[strings.TrimSuffix(ctx, "s")]
	}
	return tokens[ctx+"s"]
}

func rawContextTokenSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, tok := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	}) {
		tok = strings.TrimSpace(tok)
		if tok != "" {
			out[tok] = true
		}
	}
	return out
}

var legacyAdaptationMetaTokens = map[string]bool{
	"anti":     true,
	"aware":    true,
	"consumed": true,
	"context":  true,
	"material": true,
	"pattern":  true,
	"patterns": true,
	"proof":    true,
	"runtime":  true,
}

func pruneLegacyAdaptationMetaTokens(tokens map[string]bool, task string) {
	if !legacyAdaptationTask(task) {
		return
	}
	for tok := range legacyAdaptationMetaTokens {
		delete(tokens, tok)
	}
}

func legacyAdaptationTask(task string) bool {
	text := strings.ToLower(task)
	hasLegacySubject := strings.Contains(text, "operating-system") ||
		strings.Contains(text, "legacy") ||
		strings.Contains(text, "old system") ||
		strings.Contains(text, "old-system") ||
		strings.Contains(text, "old operating")
	if !hasLegacySubject {
		return false
	}
	return strings.Contains(text, "adapt") ||
		strings.Contains(text, "absorb") ||
		strings.Contains(text, "migrat") ||
		strings.Contains(text, "translate") ||
		strings.Contains(text, "port")
}

func surfaceTriggerScore(taskTokens map[string]bool, n Node) (float64, []string) {
	matched := map[string]bool{}
	for _, trigger := range n.Triggers {
		triggerTokens := tokenList(trigger)
		triggerSet := map[string]bool{}
		for _, tok := range triggerTokens {
			triggerSet[tok] = true
		}
		for _, tok := range triggerTokens {
			if tok == "golang" && taskTokens[goLanguageAliasToken] && !goLanguageCapability(n, triggerSet) {
				continue
			}
			if tok == "go" && taskTokens["golang"] && !triggerSet["golang"] && !hasAnyToken(triggerSet, goEngineeringContextTokens) {
				continue
			}
			if taskTokens[tok] {
				matched[tok] = true
			}
		}
	}
	tokens := make([]string, 0, len(matched))
	for tok := range matched {
		tokens = append(tokens, tok)
	}
	sort.Strings(tokens)
	return float64(len(tokens)), tokens
}

func appendMatchedTokens(tokens []string, values ...string) []string {
	seen := map[string]bool{}
	for _, token := range tokens {
		seen[token] = true
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		tokens = append(tokens, value)
		seen[value] = true
	}
	sort.Strings(tokens)
	return tokens
}

func appendUniqueString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func tokenList(s string) []string {
	var out []string
	for _, tok := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	}) {
		if usableCapabilityToken(tok) {
			out = append(out, tok)
		}
	}
	return out
}

func usableCapabilityToken(tok string) bool {
	tok = strings.TrimSpace(strings.ToLower(tok))
	return tok != "" && !capabilityTokenStopWords[tok]
}

func hasAnyToken(tokens map[string]bool, candidates map[string]bool) bool {
	for tok := range candidates {
		if tokens[tok] {
			return true
		}
	}
	return false
}

const goLanguageAliasToken = "__compound_go_language_alias"

func goLanguageCapability(n Node, triggerSet map[string]bool) bool {
	id := strings.ToLower(n.ID + " " + n.Name)
	return strings.Contains(id, "golang") || strings.Contains(id, "gochi") || triggerSet["go"]
}

var goEngineeringContextTokens = map[string]bool{
	"api": true, "backend": true, "cli": true, "code": true, "concurrency": true,
	"database": true, "distiller": true, "endpoint": true, "function": true,
	"goroutine": true, "middleware": true, "migration": true, "module": true,
	"package": true, "repo": true, "repository": true, "service": true,
	"struct": true, "test": true, "tests": true,
}

var capabilityTokenStopWords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true,
	"be": true, "before": true, "by": true, "for": true, "from": true,
	"in": true, "into": true, "is": true, "it": true, "its": true,
	"of": true, "on": true, "or": true, "that": true, "the": true,
	"this": true, "to": true, "use": true, "uses": true, "using": true,
	"via": true, "when": true, "where": true, "with": true,
	"without": true,
	"build":   true, "building": true, "built": true,
	"feature": true, "features": true, "implement": true, "implementation": true,
	"request": true, "requests": true, "replay": true,

	// Capability-graph meta words describe the registry itself. Keeping them as
	// match tokens turns "choose a skill/tool/capability" into "offer almost
	// everything", which defeats task-conditioned surfacing.
	"capability": true, "capabilities": true, "skill": true, "skills": true,
	"mcp": true, "plugin": true, "plugins": true,
	"task": true, "tasks": true, "tool": true, "tools": true,
	"workflow": true, "workflows": true,
	// Control-plane adaptation prompts often mention old systems and instructions
	// generically. The private adaptation doctrine carries that trigger; graph
	// surfacing should require a more specific capability token.
	"everything": true, "instruction": true, "instructions": true,
	"operating": true, "prompt": true, "prompts": true, "system": true, "systems": true,
	// Agent describes the caller/role across too many tools and skills. Let
	// concrete task words like harness, adapter, workbench, browser, or MCP match.
	"agent": true, "agents": true,
}

func appendJSONL(path string, v any) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

func readJSONL(path string, fn func([]byte) error) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if err := fn([]byte(line)); err != nil {
			return err
		}
	}
	return nil
}

func contains(set []string, v string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

func capabilityNodeID(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.TrimPrefix(ref, "capability://")
	return strings.TrimSpace(ref)
}

func receiptEdge(kind, from, to, receiptID, provenance string, weight float64, now time.Time) Edge {
	return Edge{
		Kind:          kind,
		From:          from,
		To:            to,
		Confidence:    1.0,
		Weight:        weight,
		Provenance:    provenance,
		SourceReceipt: strings.TrimSpace(receiptID),
		CreatedAt:     now.UTC().Format(time.RFC3339),
	}
}

func receiptEdgeProvenance(rec envelope.Receipt) (string, float64) {
	const (
		cheapLaneWeight    = 1.0
		verifiedLaneWeight = 2.0
	)
	parts := []string{rec.Producer.Kind, rec.Producer.Name}
	parts = append(parts, rec.Cost.ModelCalls...)
	parts = append(parts, rec.ProofRefs...)
	text := strings.ToLower(strings.Join(parts, " "))
	if strings.Contains(text, "verified") ||
		strings.Contains(text, "oracle") ||
		strings.Contains(text, "human-reviewed") ||
		strings.Contains(text, "human_reviewed") {
		return "verified_lane", verifiedLaneWeight
	}
	if strings.Contains(text, "local") {
		return "local_lane", cheapLaneWeight
	}
	return "cheap_lane", cheapLaneWeight
}

func edgeScore(e Edge) float64 {
	conf := e.Confidence
	if conf == 0 {
		conf = 1
	}
	weight := e.Weight
	if weight == 0 {
		weight = 1
	}
	return conf * weight
}

func sortConstraints(values []SurfacingConstraint) {
	sort.Slice(values, func(i, j int) bool {
		if values[i].Kind != values[j].Kind {
			return values[i].Kind < values[j].Kind
		}
		if values[i].Ref != values[j].Ref {
			return values[i].Ref < values[j].Ref
		}
		return values[i].Detail < values[j].Detail
	})
}

func edgeKey(e Edge) string {
	key := e.Kind + "|" + e.From + "|" + e.To
	if strings.TrimSpace(e.SourceReceipt) != "" {
		key += "|" + strings.TrimSpace(e.SourceReceipt)
	}
	return key
}
