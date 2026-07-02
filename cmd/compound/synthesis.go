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
	WorkID        string                   `json:"work_id"`
	Report        mem.SynthesisSweepReport `json:"report"`
}

type synthesisContradictionArtifact struct {
	SchemaVersion string                             `json:"schema_version"`
	ID            string                             `json:"id"`
	WorkID        string                             `json:"work_id"`
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
		WorkID:        created.ID,
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
			WorkID:        created.ID,
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
