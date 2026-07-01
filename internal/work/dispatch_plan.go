package work

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

const DispatchPlanSchemaVersion = "compound.work_dispatch_plan.v0.1"

type DispatchPlanOptions struct {
	Actor                 string
	Record                bool
	RecentLimit           int
	SourcePacketPath      string
	SourcePacketState     string
	SourcePacketOwner     string
	SourcePacketRisk      string
	SourcePacketPrivacy   string
	SourcePacketContextID string
}

type DispatchPlan struct {
	SchemaVersion string              `json:"schema_version"`
	ID            string              `json:"id"`
	GeneratedAt   string              `json:"generated_at"`
	Status        string              `json:"status"`
	Mode          string              `json:"mode"`
	Actor         string              `json:"actor"`
	PlanPath      string              `json:"plan_path,omitempty"`
	Source        *DispatchPlanSource `json:"source,omitempty"`
	Counts        WorkbenchCounts     `json:"counts"`
	Selected      *DispatchPlanItem   `json:"selected,omitempty"`
	Candidates    []DispatchPlanItem  `json:"candidates"`
	NoAction      string              `json:"no_action,omitempty"`
	Warnings      []string            `json:"warnings,omitempty"`
	Integrity     []string            `json:"integrity,omitempty"`
}

type DispatchPlanSource struct {
	Kind         string `json:"kind"`
	Path         string `json:"path"`
	Created      bool   `json:"created"`
	WorkID       string `json:"work_id"`
	State        string `json:"state"`
	PacketTitle  string `json:"packet_title,omitempty"`
	EvidenceRef  string `json:"evidence_ref,omitempty"`
	SourceDigest string `json:"source_digest,omitempty"`
}

type DispatchPlanItem struct {
	WorkID           string   `json:"work_id"`
	State            string   `json:"state"`
	Owner            string   `json:"owner,omitempty"`
	SuccessPredicate string   `json:"success_predicate,omitempty"`
	DesiredOutcome   string   `json:"desired_outcome,omitempty"`
	Action           string   `json:"action"`
	Command          []string `json:"command,omitempty"`
	Reason           string   `json:"reason"`
	NextAction       string   `json:"next_action,omitempty"`
	EvidenceRefs     []string `json:"evidence_refs,omitempty"`
}

func (s *Store) DispatchPlan(now time.Time, opts DispatchPlanOptions) (DispatchPlan, error) {
	actor := strings.TrimSpace(opts.Actor)
	if actor == "" {
		actor = "codex"
	}
	var source *DispatchPlanSource
	if strings.TrimSpace(opts.SourcePacketPath) != "" {
		state := strings.TrimSpace(opts.SourcePacketState)
		if state == "" {
			state = "ready"
		}
		imported, err := s.ImportPacket(opts.SourcePacketPath, PacketImportOptions{
			Owner:         opts.SourcePacketOwner,
			State:         state,
			Risk:          opts.SourcePacketRisk,
			Privacy:       opts.SourcePacketPrivacy,
			ContextPackID: opts.SourcePacketContextID,
		}, now)
		if err != nil {
			return DispatchPlan{}, err
		}
		source = &DispatchPlanSource{
			Kind:         "packet",
			Path:         opts.SourcePacketPath,
			Created:      imported.Created,
			WorkID:       imported.Work.ID,
			State:        imported.Work.State,
			PacketTitle:  imported.PacketTitle,
			EvidenceRef:  imported.EvidenceRef,
			SourceDigest: imported.SourceDigest,
		}
	}
	report, err := s.Workbench(now, WorkbenchOptions{RecentLimit: opts.RecentLimit})
	if err != nil {
		return DispatchPlan{}, err
	}
	plan := DispatchPlan{
		SchemaVersion: DispatchPlanSchemaVersion,
		ID:            idFor("wdp_", actor, ts(now)),
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		Status:        "no_open_work",
		Mode:          "proposal_only",
		Actor:         actor,
		Source:        source,
		Counts:        report.Counts,
		Candidates:    []DispatchPlanItem{},
		Warnings: []string{
			"does not claim work",
			"does not execute work",
			"does not evaluate policy",
			"does not close work",
			"source packet import creates only source-backed WorkEnvelope state",
		},
		Integrity: append([]string{}, report.Integrity...),
	}
	for _, item := range report.Open {
		candidate := dispatchCandidate(item, actor)
		plan.Candidates = append(plan.Candidates, candidate)
		if plan.Selected == nil && candidate.Action == "claim" {
			selected := candidate
			plan.Selected = &selected
			plan.Status = "ready_to_claim"
		}
	}
	if plan.Selected == nil {
		switch {
		case len(plan.Candidates) == 0:
			plan.NoAction = "workbench has no open work"
		case report.Counts.NeedsReview > 0:
			plan.Status = "needs_review"
			plan.NoAction = "open work exists but requires review before dispatch"
		case report.Counts.InProgress > 0:
			plan.Status = "in_progress_only"
			plan.NoAction = "open work is already claimed or running"
		default:
			plan.Status = "no_claimable_work"
			plan.NoAction = "open work is not ready to claim"
		}
	}
	if opts.Record {
		path, err := s.RecordDispatchPlan(plan)
		if err != nil {
			return DispatchPlan{}, err
		}
		plan.PlanPath = path
	}
	return plan, nil
}

func (s *Store) RecordDispatchPlan(plan DispatchPlan) (string, error) {
	if strings.TrimSpace(plan.ID) == "" {
		return "", os.ErrInvalid
	}
	dir := filepath.Join(s.home, "dispatch-plans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, plan.ID+".json")
	if err := s.writeJSON(path, &plan); err != nil {
		return "", err
	}
	return path, nil
}

func dispatchCandidate(item WorkbenchItem, actor string) DispatchPlanItem {
	out := DispatchPlanItem{
		WorkID:           item.WorkID,
		State:            item.State,
		Owner:            item.Owner,
		SuccessPredicate: item.SuccessPredicate,
		DesiredOutcome:   item.DesiredOutcome,
		Reason:           item.OpenReason,
		NextAction:       item.NextAction,
		EvidenceRefs:     append([]string{}, item.EvidenceRefs...),
	}
	switch item.State {
	case "ready":
		out.Action = "claim"
		out.Command = []string{"compound", "work", "claim", item.WorkID, "--by", actor}
	case "proposed", "admitted":
		out.Action = "prepare"
	case "held", "blocked":
		out.Action = "review"
	case "claimed", "running", "verifying":
		out.Action = "observe"
	default:
		out.Action = "inspect"
	}
	return out
}
