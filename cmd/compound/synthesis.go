package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"compound/internal/home"
	"compound/internal/mem"
	"compound/internal/reviewartifact"
	"compound/internal/work"
)

const (
	synthesisSweepArea         = "synthesis-sweeps"
	synthesisContradictionArea = "synthesis-contradictions"
)

type synthesisSweepOutput struct {
	SchemaVersion            string                   `json:"schema_version"`
	WorkID                   string                   `json:"work_id"`
	ReceiptID                string                   `json:"receipt_id"`
	TransitionID             string                   `json:"transition_id"`
	ReportPath               string                   `json:"report_path"`
	ContradictionReviewPaths []string                 `json:"contradiction_review_paths,omitempty"`
	Report                   mem.SynthesisSweepReport `json:"report"`
}

type synthesisSweepArtifact struct {
	SchemaVersion string                   `json:"schema_version"`
	ID            string                   `json:"id"`
	CreatedAt     string                   `json:"created_at"`
	Status        string                   `json:"status"`
	Readiness     string                   `json:"readiness"`
	WorkID        string                   `json:"work_id"`
	Owner         string                   `json:"owner,omitempty"`
	SourceKind    string                   `json:"source_kind,omitempty"`
	NextAction    string                   `json:"next_action,omitempty"`
	NotToDo       string                   `json:"not_to_do,omitempty"`
	Boundaries    []string                 `json:"boundaries,omitempty"`
	EvidenceRefs  []string                 `json:"evidence_refs,omitempty"`
	Report        mem.SynthesisSweepReport `json:"report"`
}

type synthesisContradictionArtifact struct {
	SchemaVersion string                             `json:"schema_version"`
	ID            string                             `json:"id"`
	CreatedAt     string                             `json:"created_at"`
	Status        string                             `json:"status"`
	Readiness     string                             `json:"readiness"`
	WorkID        string                             `json:"work_id"`
	Owner         string                             `json:"owner,omitempty"`
	SourceKind    string                             `json:"source_kind,omitempty"`
	NextAction    string                             `json:"next_action,omitempty"`
	NotToDo       string                             `json:"not_to_do,omitempty"`
	Boundaries    []string                           `json:"boundaries,omitempty"`
	EvidenceRefs  []string                           `json:"evidence_refs,omitempty"`
	Proposal      mem.SynthesisContradictionProposal `json:"proposal"`
}

func cmdSynthesis(args []string) {
	if len(args) == 0 || args[0] != "sweep" {
		fmt.Fprintln(os.Stderr, "usage: compound synthesis sweep [--stage cluster,contradict] [--json]")
		os.Exit(2)
	}
	var stages []string
	jsonOut := false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOut = true
		case "--stage":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "error: --stage requires value")
				os.Exit(2)
			}
			i++
			stages = append(stages, args[i])
		default:
			fmt.Fprintln(os.Stderr, "unknown synthesis sweep arg:", args[i])
			os.Exit(2)
		}
	}
	out, err := runSynthesisSweep(stages, time.Now())
	die(err)
	if jsonOut {
		printJSON(out)
		return
	}
	fmt.Printf("synthesis sweep work=%s receipt=%s transition=%s clusters=%d echo_fold=%d contradictions=%d report=%s\n",
		out.WorkID, out.ReceiptID, out.TransitionID, out.Report.ClusterCount, out.Report.EchoFoldProposalCount, out.Report.ContradictionProposalCount, out.ReportPath)
}

func runSynthesisSweep(stages []string, now time.Time) (synthesisSweepOutput, error) {
	var out synthesisSweepOutput
	homePath, err := home.Resolve("")
	if err != nil {
		return out, err
	}
	memStore, err := mem.Open(homePath)
	if err != nil {
		return out, err
	}
	workStore, err := work.Open(homePath)
	if err != nil {
		return out, err
	}
	created, err := workStore.Create(work.CreateInput{
		Owner:            "compound synthesis",
		Actor:            "compound synthesis",
		State:            "ready",
		DesiredOutcome:   "proposal-only memory synthesis sweep report",
		SuccessPredicate: "synthesis sweep emits report artifacts without mutating memory candidate inbox or claims ledger",
		EvidenceRefs:     []string{"compound://synthesis/sweep"},
	}, now)
	if err != nil {
		return out, err
	}
	if _, err := workStore.Claim(created.ID, "compound synthesis", now); err != nil {
		return out, err
	}
	if _, err := workStore.Advance(created.ID, work.AdvanceInput{To: "running", Actor: "compound synthesis", Note: "running proposal-only synthesis sweep"}, now); err != nil {
		return out, err
	}
	report, err := memStore.SynthesisSweep(stages, now)
	if err != nil {
		return out, err
	}
	reportID := reviewartifact.ID("synthesis_sweep_", created.ID, strings.Join(report.Stages, ","), report.GeneratedAt)
	reportArtifact := synthesisSweepArtifact{
		SchemaVersion: "compound.synthesis_sweep_artifact.v0.1",
		ID:            reportID,
		CreatedAt:     reviewartifact.Timestamp(now),
		Status:        "recorded",
		Readiness:     "review_recorded",
		WorkID:        created.ID,
		Owner:         "Memory synthesis",
		SourceKind:    "memory-synthesis-sweep",
		NextAction:    synthesisSweepNextAction,
		NotToDo:       synthesisNotToDo,
		Boundaries:    synthesisReviewBoundaries(),
		EvidenceRefs:  []string{"compound://synthesis/sweep", created.ID},
		Report:        report,
	}
	reportPaths, err := reviewartifact.Write(homePath, synthesisSweepArea, reportID, reportArtifact, " ", renderSynthesisSweepMarkdown(reportArtifact))
	if err != nil {
		return out, err
	}
	var contradictionPaths []string
	for _, proposal := range report.ContradictionProposals {
		reviewID := reviewartifact.ID("synthesis_contradiction_", created.ID, proposal.ID)
		artifact := synthesisContradictionArtifact{
			SchemaVersion: "compound.synthesis_contradiction_review.v0.1",
			ID:            reviewID,
			CreatedAt:     reviewartifact.Timestamp(now),
			Status:        "recorded",
			Readiness:     "review_recorded",
			WorkID:        created.ID,
			Owner:         "Memory synthesis",
			SourceKind:    "memory-synthesis-contradiction",
			NextAction:    synthesisContradictionNextAction,
			NotToDo:       synthesisNotToDo,
			Boundaries:    synthesisReviewBoundaries(),
			EvidenceRefs:  synthesisContradictionEvidenceRefs(created.ID, proposal),
			Proposal:      proposal,
		}
		paths, err := reviewartifact.Write(homePath, synthesisContradictionArea, reviewID, artifact, " ", renderSynthesisContradictionMarkdown(artifact))
		if err != nil {
			return out, err
		}
		contradictionPaths = append(contradictionPaths, paths.PlanPath)
	}
	if _, err := workStore.Advance(created.ID, work.AdvanceInput{
		To:             "verifying",
		Actor:          "compound synthesis",
		ArtifactID:     reportID,
		VerificationID: "synthesis_sweep_report",
		Note:           "proposal-only sweep report written",
	}, now); err != nil {
		return out, err
	}
	closed, err := workStore.Close(created.ID, work.CloseInput{
		Outcome:   "completed",
		MoveType:  "audit_only",
		Actor:     "compound synthesis",
		Summary:   "proposal-only memory synthesis sweep completed without memory authority mutation",
		ProofRefs: []string{reportID, "synthesis_sweep_report"},
	}, now)
	if err != nil {
		return out, err
	}
	return synthesisSweepOutput{
		SchemaVersion:            "compound.synthesis_sweep_run.v0.1",
		WorkID:                   created.ID,
		ReceiptID:                closed.Receipt.ID,
		TransitionID:             closed.Transition.ID,
		ReportPath:               reportPaths.PlanPath,
		ContradictionReviewPaths: contradictionPaths,
		Report:                   report,
	}, nil
}

func renderSynthesisSweepMarkdown(a synthesisSweepArtifact) string {
	return fmt.Sprintf("# Synthesis sweep\n\n- work: `%s`\n- stages: `%s`\n- clusters: %d\n- echo_fold_proposals: %d\n- contradiction_proposals: %d\n",
		a.WorkID, strings.Join(a.Report.Stages, ","), a.Report.ClusterCount, a.Report.EchoFoldProposalCount, a.Report.ContradictionProposalCount)
}

func renderSynthesisContradictionMarkdown(a synthesisContradictionArtifact) string {
	return fmt.Sprintf("# Synthesis contradiction proposal\n\n- work: `%s`\n- cluster_key: `%s`\n- claims: `%s`\n- objects: `%s`\n",
		a.WorkID, a.Proposal.ClusterKey, strings.Join(a.Proposal.ClaimIDs, ","), strings.Join(a.Proposal.Objects, ","))
}

const (
	synthesisSweepNextAction         = "synthesis sweep recorded; inspect proposals and create a focused WorkEnvelope only for one evidence-backed repair"
	synthesisContradictionNextAction = "review the claim group; create a focused memory or decision repair packet only if current evidence proves a supersession"
	synthesisNotToDo                 = "do not bulk-apply, auto-adjudicate, promote, reject, or supersede memory from synthesis proposals alone"
)

func synthesisReviewBoundaries() []string {
	return []string{
		"proposal-only",
		"no memory mutation",
		"no candidate adjudication",
		"no claim promotion",
		"no claim supersession",
	}
}

func synthesisContradictionEvidenceRefs(workID string, proposal mem.SynthesisContradictionProposal) []string {
	refs := []string{"compound://synthesis/sweep", workID, proposal.ID}
	for _, id := range proposal.ClaimIDs {
		refs = append(refs, "claim:"+id)
	}
	return reviewartifact.Unique(refs)
}

func annotateSynthesisReviewQueue(report *reviewartifact.QueueReport) {
	if report == nil {
		return
	}
	for i := range report.Items {
		item := &report.Items[i]
		switch item.Area {
		case synthesisSweepArea:
			annotateSynthesisQueueItem(item, "memory-synthesis-sweep", synthesisSweepNextAction)
		case synthesisContradictionArea:
			annotateSynthesisQueueItem(item, "memory-synthesis-contradiction", synthesisContradictionNextAction)
		}
	}
}

func annotateSynthesisQueueItem(item *reviewartifact.QueueItem, sourceKind, nextAction string) {
	if item.Status == "" || item.Status == "unknown" {
		item.Status = "recorded"
	}
	item.Owner = reviewartifact.FirstNonEmpty(item.Owner, "Memory synthesis")
	item.SourceKind = reviewartifact.FirstNonEmpty(item.SourceKind, sourceKind)
	item.Readiness = reviewartifact.FirstNonEmpty(item.Readiness, "review_recorded")
	item.NextAction = reviewartifact.FirstNonEmpty(item.NextAction, nextAction)
	item.NotToDo = reviewartifact.FirstNonEmpty(item.NotToDo, synthesisNotToDo)
	item.Boundaries = reviewartifact.Unique(append(item.Boundaries, synthesisReviewBoundaries()...))
	item.Actions = nil
}
