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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"compound/internal/candidates"
)

// NodeKinds — skills are one kind, not the whole graph.
var NodeKinds = []string{"lens", "procedure", "script", "tool", "mcp", "model_profile", "harness", "eval", "workflow", "adapter", "doctrine", "taste_standard", "skill"}

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
)

var EdgeKinds = []string{EdSurfacesWith, EdRequires, EdConflictsWith, EdSpecializes, EdFallbackFor, EdValidatedBy, EdMissedWhen, EdFalseTriggeredBy, EdRetiredBy}

// FeedbackEdges are the ones a CapabilityObservation can write (context:<token> → node).
var FeedbackEdges = map[string]bool{EdMissedWhen: true, EdFalseTriggeredBy: true}

// Node is a capability. Triggers are the task tokens that surface it; Weight is its base surfacing
// strength (learned feedback adds/subtracts on top).
type Node struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	Name     string   `json:"name"`
	Triggers []string `json:"triggers"`
	Weight   float64  `json:"weight"`
	Retired  bool     `json:"retired,omitempty"`
}

// Edge is a typed relation. For feedback edges From is "context:<token>" and To is a node id.
type Edge struct {
	Kind       string  `json:"kind"`
	From       string  `json:"from"`
	To         string  `json:"to"`
	Confidence float64 `json:"confidence"`
	CreatedAt  string  `json:"created_at"`
}

type Store struct{ home string }

func Open(homePath string) (*Store, error) {
	if strings.TrimSpace(homePath) == "" {
		return nil, fmt.Errorf("no COMPOUND_HOME (run `compound home init`)")
	}
	if err := os.MkdirAll(filepath.Join(homePath, "registries"), 0o755); err != nil {
		return nil, err
	}
	return &Store{home: homePath}, nil
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
	Node  Node    `json:"node"`
	Score float64 `json:"score"`
	Why   string  `json:"why"`
}

// Surface returns the capability nodes relevant to a task, ranked — and runs NOTHING. Base score
// is trigger overlap × weight; a missed_when edge for a present context token ADDS to the score
// (learned surfacing); a false_triggered_by edge SUBTRACTS. Retired nodes never surface.
func (s *Store) Surface(task string) ([]Surfaced, error) {
	nodes, err := s.loadNodes()
	if err != nil {
		return nil, err
	}
	edges, err := s.loadEdges()
	if err != nil {
		return nil, err
	}
	tokens := tokenSet(task)

	// learned context → node adjustments
	add := map[string]float64{}
	sub := map[string]float64{}
	for _, e := range edges {
		ctx := strings.TrimPrefix(e.From, "context:")
		if e.From == ctx || !tokens[ctx] { // not a context edge, or its token isn't in the task
			continue
		}
		switch e.Kind {
		case EdMissedWhen:
			add[e.To] += e.Confidence
		case EdFalseTriggeredBy:
			sub[e.To] += e.Confidence
		}
	}

	var out []Surfaced
	for _, n := range nodes {
		if n.Retired {
			continue
		}
		base := 0.0
		for _, tr := range n.Triggers {
			if tokens[strings.ToLower(tr)] {
				base++
			}
		}
		score := base*n.Weight + add[n.ID] - sub[n.ID]
		if score <= 0 {
			continue
		}
		why := fmt.Sprintf("trigger×weight=%.1f", base*n.Weight)
		if add[n.ID] > 0 {
			why += fmt.Sprintf(" +missed_when=%.1f", add[n.ID])
		}
		if sub[n.ID] > 0 {
			why += fmt.Sprintf(" -false_triggered=%.1f", sub[n.ID])
		}
		out = append(out, Surfaced{Node: n, Score: score, Why: why})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Node.ID < out[j].Node.ID
	})
	return out, nil
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

func (s *Store) List() ([]Node, error) { return s.loadNodes() }

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

// loadEdges dedups last-wins by (kind, from, to), so re-Consuming the same observation is
// IDEMPOTENT (it does not double-count a feedback edge); distinct contexts still accumulate.
func (s *Store) loadEdges() ([]Edge, error) {
	byKey := map[string]Edge{}
	var order []string
	err := readJSONL(s.edgesLog(), func(line []byte) error {
		var e Edge
		if err := json.Unmarshal(line, &e); err != nil {
			return nil // skip a torn/malformed line
		}
		k := e.Kind + "|" + e.From + "|" + e.To
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

func tokenSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, t := range tokenList(s) {
		out[t] = true
	}
	return out
}

func tokenList(s string) []string {
	var out []string
	for _, t := range strings.Fields(strings.ToLower(s)) {
		if tok := strings.Trim(t, ".,;:!?\"'()"); tok != "" {
			out = append(out, tok)
		}
	}
	return out
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
