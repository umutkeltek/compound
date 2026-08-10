package capgraph

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/envelope"
	"compound/internal/policy"
)

const CapabilityGraphGovernanceSchemaVersion = "compound.capability_graph_governance.v0.1"

// CapabilityGraphGovernanceRecord is the durable audit pair for every capgraph
// mutation that crosses the ActionIntent + PolicyDecision seam. It is append-only
// so a reader can reconstruct which intents were allowed or denied without trusting
// the mutating code path.
type CapabilityGraphGovernanceRecord struct {
	SchemaVersion    string   `json:"schema_version"`
	ID               string   `json:"id"`
	CreatedAt        string   `json:"created_at"`
	ActionIntentID   string   `json:"action_intent_id"`
	PolicyDecisionID string   `json:"policy_decision_id"`
	PolicyOutcome    string   `json:"policy_outcome"`
	Allowed          bool     `json:"allowed"`
	MutationKind     string   `json:"mutation_kind"`
	NodeIDs          []string `json:"node_ids,omitempty"`
	EdgeIDs          []string `json:"edge_ids,omitempty"`
	ManifestID       string   `json:"manifest_id,omitempty"`
}

type capabilityGraphMutation struct {
	kind       string
	nodeIDs    []string
	edgeIDs    []string
	manifestID string
	apply      func(*Store) error
}

// AddGoverned is the fail-closed entry point for adding a capability node. It
// mints an ActionIntent, evaluates policy, records the governance pair, and only
// then appends the node. A configured policy deny prevents the write; a retired
// node cannot be resurrected silently.
func AddGoverned(homePath string, node Node, now time.Time) error {
	if err := guardNoResurrect(homePath, []Node{node}); err != nil {
		return err
	}
	mutation := capabilityGraphMutation{
		kind:    "add_node",
		nodeIDs: []string{node.ID},
		apply: func(s *Store) error {
			return s.Add(node)
		},
	}
	return applyGovernedCapabilityGraphMutation(homePath, mutation, now)
}

// RetireGoverned is the fail-closed entry point for retiring capability nodes.
func RetireGoverned(homePath string, ids []string, now time.Time) ([]string, error) {
	var retired []string
	mutation := capabilityGraphMutation{
		kind:    "retire_nodes",
		nodeIDs: append([]string(nil), ids...),
		apply: func(s *Store) error {
			var err error
			retired, err = s.Retire(ids...)
			return err
		},
	}
	if err := applyGovernedCapabilityGraphMutation(homePath, mutation, now); err != nil {
		return retired, err
	}
	return retired, nil
}

// ObserveGoverned is the fail-closed entry point for recording feedback edges.
func ObserveGoverned(homePath string, node, signal, context string, conf float64, now time.Time) ([]Edge, error) {
	var edges []Edge
	mutation := capabilityGraphMutation{
		kind: "observe",
		apply: func(s *Store) error {
			var err error
			edges, err = s.Observe(node, signal, context, conf, now)
			return err
		},
	}
	if err := applyGovernedCapabilityGraphMutation(homePath, mutation, now); err != nil {
		return edges, err
	}
	return edges, nil
}

func guardNoResurrect(homePath string, proposed []Node) error {
	store, err := Open(homePath)
	if err != nil {
		return err
	}
	nodes, err := store.loadNodes()
	if err != nil {
		return err
	}
	retired := map[string]bool{}
	for _, n := range nodes {
		if n.Retired {
			retired[n.ID] = true
		}
	}
	for _, n := range proposed {
		if retired[n.ID] && !n.Retired {
			return fmt.Errorf("capability graph mutation would resurrect retired node %s; use a new identity or an explicit lifecycle review", n.ID)
		}
	}
	return nil
}

func applyGovernedCapabilityGraphMutation(homePath string, mutation capabilityGraphMutation, now time.Time) error {
	if strings.TrimSpace(homePath) == "" {
		return fmt.Errorf("COMPOUND_HOME required")
	}
	if now.IsZero() {
		now = time.Now()
	}

	ai := capabilityGraphActionIntent(homePath, mutation, now)
	if err := ai.Validate(); err != nil {
		return fmt.Errorf("capability graph action intent invalid: %w", err)
	}
	cfg, policySource, err := policy.LoadConfig(homePath)
	if err != nil {
		return fmt.Errorf("load policy: %w", err)
	}
	pd := policy.Evaluate(ai, cfg, now)
	allowed := policy.Allowed(pd)

	if recordErr := recordCapabilityGraphGovernance(homePath, mutation, ai, pd, allowed, now); recordErr != nil {
		return fmt.Errorf("record governance: %w", recordErr)
	}
	if !allowed {
		return fmt.Errorf("policy %s (%s) refuses capability graph %s mutation; source=%s", pd.Outcome, pd.ID, mutation.kind, policySource)
	}

	store, err := Open(homePath)
	if err != nil {
		return err
	}
	return mutation.apply(store)
}

func capabilityGraphActionIntent(homePath string, mutation capabilityGraphMutation, now time.Time) envelope.ActionIntent {
	mutationClass := "local_reversible"
	reversibility := "easy"
	blastRadius := "local"
	risk := "tier1"
	switch mutation.kind {
	case "retire_nodes":
		mutationClass = "destructive"
		reversibility = "irreversible"
		risk = "tier3"
	case "add_node", "observe", "apply_skill_manifest":
		// defaults above
	}

	refs := []string{"capability_graph:" + homePath}
	targetPath := filepath.Join(homePath, "registries", "capability-nodes.jsonl")
	if len(mutation.edgeIDs) > 0 || mutation.kind == "observe" {
		targetPath = filepath.Join(homePath, "registries", "capability-edges.jsonl")
	}

	summary := mutation.kind
	if mutation.manifestID != "" {
		summary += " manifest=" + mutation.manifestID
	}
	if len(mutation.nodeIDs) > 0 {
		summary += " nodes=" + strings.Join(mutation.nodeIDs, ",")
	}
	if len(mutation.edgeIDs) > 0 {
		summary += " edges=" + strings.Join(mutation.edgeIDs, ",")
	}

	id := capabilityGraphActionIntentID(mutation, now)
	return envelope.ActionIntent{
		Header: envelope.Header{
			SchemaVersion: envelope.SVActionIntent,
			ID:            id,
			CreatedAt:     now.UTC().Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "capability-graph", Name: "compound"},
			IDs:           envelope.IDs{WorkID: "work_capgraph_mutation"},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          risk,
			EvidenceRefs:  refs,
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Actor:         "capability-graph",
		Capability:    "capability_graph." + mutation.kind,
		Action:        mutation.kind,
		Target:        envelope.ActionTarget{Kind: "capability_graph", Path: targetPath},
		MutationClass: mutationClass,
		Reversibility: reversibility,
		BlastRadius:   blastRadius,
		Preview:       summary,
		RollbackPlan:  "restore registries/capability-nodes.jsonl and registries/capability-edges.jsonl from backup",
	}
}

func capabilityGraphActionIntentID(mutation capabilityGraphMutation, now time.Time) string {
	h := sha256.New()
	fmt.Fprint(h, mutation.kind, "\n")
	for _, id := range mutation.nodeIDs {
		fmt.Fprint(h, id, "\n")
	}
	for _, id := range mutation.edgeIDs {
		fmt.Fprint(h, id, "\n")
	}
	fmt.Fprint(h, mutation.manifestID, "\n")
	fmt.Fprint(h, now.UTC().Format(time.RFC3339Nano), "\n")
	return "act_capgraph_" + hex.EncodeToString(h.Sum(nil))[:12]
}

func recordCapabilityGraphGovernance(homePath string, mutation capabilityGraphMutation, ai envelope.ActionIntent, pd envelope.PolicyDecision, allowed bool, now time.Time) error {
	rec := CapabilityGraphGovernanceRecord{
		SchemaVersion:    CapabilityGraphGovernanceSchemaVersion,
		ID:               capabilityGraphGovernanceID(ai, pd),
		CreatedAt:        now.UTC().Format(time.RFC3339),
		ActionIntentID:   ai.ID,
		PolicyDecisionID: pd.ID,
		PolicyOutcome:    pd.Outcome,
		Allowed:          allowed,
		MutationKind:     mutation.kind,
		NodeIDs:          cleanSorted(mutation.nodeIDs),
		EdgeIDs:          cleanSorted(mutation.edgeIDs),
		ManifestID:       mutation.manifestID,
	}
	path := capabilityGraphGovernanceLog(homePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return appendJSONL(path, &rec)
}

func capabilityGraphGovernanceID(ai envelope.ActionIntent, pd envelope.PolicyDecision) string {
	sum := sha256.Sum256([]byte(ai.ID + "|" + pd.ID))
	return "capgov_" + hex.EncodeToString(sum[:])[:12]
}

func capabilityGraphGovernanceLog(homePath string) string {
	return filepath.Join(homePath, "registries", "capability-governance.jsonl")
}
