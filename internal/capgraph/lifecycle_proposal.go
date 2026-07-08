package capgraph

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"compound/internal/reviewartifact"
)

const (
	CapabilityLifecycleProposalSchemaVersion = "compound.capability.lifecycle_proposal.v0.1"
	CapabilityLifecycleProposalReviewArea    = "capability-lifecycle-proposals"
)

// CapabilityLifecycleProposal is an owner-review artifact for revising or
// retiring CapabilityGraph behavior. It never mutates graph nodes, installs
// skills, executes tools, or promotes memory truth.
type CapabilityLifecycleProposal struct {
	SchemaVersion string                           `json:"schema_version"`
	ID            string                           `json:"id"`
	CreatedAt     string                           `json:"created_at"`
	Status        string                           `json:"status"`
	ProposalKind  string                           `json:"proposal_kind"`
	Subject       string                           `json:"subject"`
	Statement     string                           `json:"statement"`
	Detail        string                           `json:"detail,omitempty"`
	Confidence    float64                          `json:"confidence,omitempty"`
	EvidenceRefs  []string                         `json:"evidence_refs"`
	Draft         CapabilityLifecycleProposalDraft `json:"draft"`
	Warnings      []string                         `json:"warnings,omitempty"`
}

type CapabilityLifecycleProposalDraft struct {
	LifecycleAction  string   `json:"lifecycle_action"`
	CapabilityRef    string   `json:"capability_ref,omitempty"`
	ProposedChange   string   `json:"proposed_change"`
	Guardrail        string   `json:"guardrail"`
	RequiredEvidence []string `json:"required_evidence"`
	ReviewNote       string   `json:"review_note"`
}

type CapabilityLifecycleProposalInput struct {
	ProposalKind  string
	Subject       string
	Statement     string
	Detail        string
	Confidence    float64
	EvidenceRefs  []string
	CapabilityRef string
}

type CapabilityLifecycleProposalPaths struct {
	PlanPath   string
	ReviewPath string
}

func BuildCapabilityLifecycleProposal(in CapabilityLifecycleProposalInput, now time.Time) (CapabilityLifecycleProposal, error) {
	kind := strings.TrimSpace(in.ProposalKind)
	if kind == "" {
		kind = "revise"
	}
	if kind != "revise" && kind != "retire" {
		return CapabilityLifecycleProposal{}, fmt.Errorf("proposal_kind must be revise or retire")
	}
	evidenceRefs := reviewartifact.Unique(in.EvidenceRefs)
	if len(evidenceRefs) == 0 {
		return CapabilityLifecycleProposal{}, fmt.Errorf("capability lifecycle proposal evidence_refs required")
	}
	statement := reviewartifact.FirstNonEmpty(in.Statement, in.Subject)
	if strings.TrimSpace(statement) == "" {
		return CapabilityLifecycleProposal{}, fmt.Errorf("capability lifecycle proposal statement required")
	}
	subject := reviewartifact.FirstNonEmpty(in.Subject, statement)
	key := reviewartifact.Slugify(reviewartifact.FirstNonEmpty(in.CapabilityRef, subject, statement), 48)
	if key == "" {
		key = reviewartifact.Digest12(statement)
	}
	id := reviewartifact.ID("clp_", kind, key, strings.Join(evidenceRefs, "\x00"))
	proposal := CapabilityLifecycleProposal{
		SchemaVersion: CapabilityLifecycleProposalSchemaVersion,
		ID:            id,
		CreatedAt:     reviewartifact.Timestamp(now),
		Status:        "review_required",
		ProposalKind:  kind,
		Subject:       subject,
		Statement:     statement,
		Detail:        strings.TrimSpace(in.Detail),
		Confidence:    in.Confidence,
		EvidenceRefs:  evidenceRefs,
		Draft: CapabilityLifecycleProposalDraft{
			LifecycleAction: kind,
			CapabilityRef:   strings.TrimSpace(in.CapabilityRef),
			ProposedChange:  statement,
			Guardrail:       "Do not mutate CapabilityGraph, skill files, adapters, or routing defaults from this proposal without an explicit reviewed owner action.",
			RequiredEvidence: []string{
				"session evidence that produced the lifecycle proposal",
				"current capability or skill behavior being revised or retired",
				"overlap/replacement scan against existing CapabilityGraph nodes",
				"validation receipt or eval proving the revised lifecycle behavior",
				"rollback or re-enable condition for any retired behavior",
			},
			ReviewNote: "This lifecycle proposal is review material only. A reviewer may revise, retire, merge, or reject it, but importing or mutating CapabilityGraph requires a separate reviewed owner path.",
		},
		Warnings: []string{
			"does not mutate CapabilityGraph",
			"does not install or edit skill files",
			"does not execute tools or MCPs",
			"does not promote memory truth",
		},
	}
	return proposal, nil
}

// AnnotateCapabilityLifecycleProposalQueue fills in the operator card for
// capability-lifecycle-proposals review artifacts. There is no separate
// applied/reviewed sidecar for this area yet (RecordCapabilityLifecycleProposal
// only ever writes the proposal itself), so every item is a still-open
// review_required decision.
func AnnotateCapabilityLifecycleProposalQueue(report *reviewartifact.QueueReport) {
	if report == nil {
		return
	}
	for i := range report.Items {
		item := &report.Items[i]
		if item.Area != CapabilityLifecycleProposalReviewArea {
			continue
		}
		annotateCapabilityLifecycleProposalItem(item)
	}
}

func annotateCapabilityLifecycleProposalItem(item *reviewartifact.QueueItem) {
	if item.JSONPath == "" {
		item.Readiness = "blocked"
		item.BlockedReason = "capability lifecycle proposal path missing"
		item.NextAction = "regenerate capability lifecycle proposal"
		return
	}
	data, err := os.ReadFile(item.JSONPath)
	if err != nil {
		item.Readiness = "blocked"
		item.BlockedReason = "capability lifecycle proposal unreadable"
		return
	}
	var proposal CapabilityLifecycleProposal
	if err := json.Unmarshal(data, &proposal); err != nil {
		item.Readiness = "blocked"
		item.BlockedReason = "capability lifecycle proposal unreadable"
		return
	}
	band := reviewartifact.BandNeedsYou
	if proposal.Status == "recorded" || proposal.Status == "resolved" || proposal.Status == "accepted" || proposal.Status == "applied" || proposal.Status == "ok" {
		band = reviewartifact.BandFiled
		item.Readiness = "no_action"
		item.NextAction = "capability lifecycle proposal recorded"
	} else {
		item.Readiness = "needs_review"
		item.NextAction = "review capability lifecycle proposal " + reviewartifact.FirstNonEmpty(proposal.Subject, proposal.ID)
	}
	card, err := OperatorCardForLifecycleProposal(proposal)
	if err != nil {
		item.BlockedReason = reviewartifact.FirstNonEmpty(item.BlockedReason, "capability lifecycle operator card invalid: "+err.Error())
		return
	}
	if err := reviewartifact.SetOperatorCard(item, card, reviewartifact.SummarySourceAnnotator, band); err != nil {
		item.BlockedReason = reviewartifact.FirstNonEmpty(item.BlockedReason, "capability lifecycle operator card invalid: "+err.Error())
	}
}

func RecordCapabilityLifecycleProposal(homePath string, proposal CapabilityLifecycleProposal) (CapabilityLifecycleProposalPaths, error) {
	if strings.TrimSpace(homePath) == "" {
		return CapabilityLifecycleProposalPaths{}, fmt.Errorf("COMPOUND_HOME required")
	}
	if strings.TrimSpace(proposal.ID) == "" {
		return CapabilityLifecycleProposalPaths{}, fmt.Errorf("capability lifecycle proposal id required")
	}
	paths, err := reviewartifact.Write(homePath, CapabilityLifecycleProposalReviewArea, proposal.ID, proposal, "  ", RenderCapabilityLifecycleProposalReview(proposal))
	return CapabilityLifecycleProposalPaths{PlanPath: paths.PlanPath, ReviewPath: paths.ReviewPath}, err
}

func RenderCapabilityLifecycleProposalReview(proposal CapabilityLifecycleProposal) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Capability Lifecycle Review: %s\n\n", proposal.ID)
	fmt.Fprintf(&b, "- status: %s\n", proposal.Status)
	fmt.Fprintf(&b, "- proposal_kind: %s\n", proposal.ProposalKind)
	fmt.Fprintf(&b, "- subject: %s\n", proposal.Subject)
	if proposal.Draft.CapabilityRef != "" {
		fmt.Fprintf(&b, "- capability_ref: %s\n", proposal.Draft.CapabilityRef)
	}
	fmt.Fprintf(&b, "- evidence_refs: %s\n\n", strings.Join(proposal.EvidenceRefs, ", "))
	fmt.Fprintln(&b, "This owner-review artifact does not mutate CapabilityGraph, install or edit skill files, execute tools/MCPs, or promote memory truth.")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Proposal")
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "- statement: %s\n", proposal.Statement)
	if proposal.Detail != "" {
		fmt.Fprintf(&b, "- detail: %s\n", proposal.Detail)
	}
	if proposal.Confidence > 0 {
		fmt.Fprintf(&b, "- confidence: %.2f\n", proposal.Confidence)
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Draft Lifecycle Change")
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "- action: %s\n", proposal.Draft.LifecycleAction)
	fmt.Fprintf(&b, "- proposed_change: %s\n", proposal.Draft.ProposedChange)
	fmt.Fprintf(&b, "- guardrail: %s\n", proposal.Draft.Guardrail)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Required Evidence Before Mutation")
	fmt.Fprintln(&b)
	for _, evidence := range proposal.Draft.RequiredEvidence {
		fmt.Fprintf(&b, "- %s\n", evidence)
	}
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "%s\n", proposal.Draft.ReviewNote)
	return b.String()
}
