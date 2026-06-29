package distiller

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"compound/internal/candidates"
	"compound/internal/writeback"
)

// DoctorCheck is one health/debt observation from the distiller read model.
type DoctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// DoctorReport summarizes the distiller's compounding state without promoting anything.
type DoctorReport struct {
	Status                                string         `json:"status"`
	TotalDecisions                        int            `json:"total_decisions"`
	DecisionsByKind                       map[string]int `json:"decisions_by_kind"`
	VerdictCounts                         map[string]int `json:"verdict_counts"`
	CandidateInboxCounts                  map[string]int `json:"candidate_inbox_counts"`
	MemoryPromoted                        int            `json:"memory_promoted"`
	MemoryParked                          int            `json:"memory_parked"`
	MemoryRejected                        int            `json:"memory_rejected"`
	DuplicateMemoryRejections             int            `json:"duplicate_memory_rejections"`
	NoPromoteRows                         int            `json:"no_promote_rows"`
	DuplicateMemoryNoPromoteRows          int            `json:"duplicate_memory_no_promote_rows"`
	EvalCases                             int            `json:"eval_cases"`
	MethodUpgradeDebt                     int            `json:"method_upgrade_debt"`
	PolicyPatchDebt                       int            `json:"policy_patch_debt"`
	CapabilityObservations                int            `json:"capability_observations"`
	OperationalizedCapabilityObservations int            `json:"operationalized_capability_observations"`
	SelfImprovementDebt                   int            `json:"self_improvement_debt"`
	Checks                                []DoctorCheck  `json:"checks"`
}

// Doctor reads COMPOUND_HOME distiller state: adjudication ledger, typed inboxes,
// and no-promote rows. It is intentionally read-only.
func Doctor(homePath string) (DoctorReport, error) {
	report := DoctorReport{
		Status:               "ok",
		DecisionsByKind:      map[string]int{},
		VerdictCounts:        map[string]int{},
		CandidateInboxCounts: map[string]int{},
	}

	decisions, err := readAdjudicationDecisions(filepath.Join(homePath, "ledger", "adjudications.jsonl"))
	if err != nil {
		return report, err
	}
	report.TotalDecisions = len(decisions)
	for _, d := range decisions {
		report.DecisionsByKind[d.Kind]++
		report.VerdictCounts[d.Verdict]++

		if d.Kind == KindMemory {
			switch d.Verdict {
			case Promote:
				report.MemoryPromoted++
			case Park:
				report.MemoryParked++
			case Reject:
				report.MemoryRejected++
			}
			if d.Verdict == Reject && d.Relation == "duplicate" {
				report.DuplicateMemoryRejections++
			}
		}
		if d.Kind == KindCapabilityObs && d.Verdict == Operationalize {
			report.OperationalizedCapabilityObservations++
		}
	}

	for _, kind := range []string{
		KindMemory,
		KindSkill,
		KindDecision,
		KindFailure,
		KindTaste,
		KindProduct,
		KindRoute,
		KindCapabilityObs,
		KindMethodUpgrade,
		KindEvalCase,
		KindPolicyPatch,
	} {
		n, err := countCandidateFiles(homePath, kind)
		if err != nil {
			return report, err
		}
		report.CandidateInboxCounts[kind] = n
	}

	report.EvalCases = report.CandidateInboxCounts[KindEvalCase]
	report.MethodUpgradeDebt = report.CandidateInboxCounts[KindMethodUpgrade]
	report.PolicyPatchDebt = report.CandidateInboxCounts[KindPolicyPatch]
	report.CapabilityObservations = report.CandidateInboxCounts[KindCapabilityObs]
	report.SelfImprovementDebt = report.MethodUpgradeDebt + report.PolicyPatchDebt

	noPromotes, err := readNoPromoteDecisions(filepath.Join(homePath, "ledger", "no-promote-decisions.jsonl"))
	if err != nil {
		return report, err
	}
	report.NoPromoteRows = len(noPromotes)
	for _, d := range noPromotes {
		if d.CandidateKind == KindMemory && d.OwnerTarget.Kind == "memory_claim" {
			report.DuplicateMemoryNoPromoteRows++
		}
	}

	report.Checks = append(report.Checks,
		DoctorCheck{
			Name:   "eval_corpus",
			Status: statusForPositive(report.EvalCases),
			Detail: fmt.Sprintf("%d eval case candidate(s)", report.EvalCases),
		},
		DoctorCheck{
			Name:   "method_policy_debt",
			Status: statusForDebt(report.SelfImprovementDebt),
			Detail: fmt.Sprintf("%d method upgrade(s), %d policy patch(es)", report.MethodUpgradeDebt, report.PolicyPatchDebt),
		},
		DoctorCheck{
			Name:   "capability_observations",
			Status: statusForPositive(report.CapabilityObservations),
			Detail: fmt.Sprintf("%d candidate(s), %d operationalized", report.CapabilityObservations, report.OperationalizedCapabilityObservations),
		},
		DoctorCheck{
			Name:   "duplicate_churn",
			Status: statusForDebt(report.DuplicateMemoryRejections),
			Detail: fmt.Sprintf("%d duplicate memory rejection(s), %d no-promote row(s)", report.DuplicateMemoryRejections, report.DuplicateMemoryNoPromoteRows),
		},
	)
	if report.SelfImprovementDebt > 0 || report.DuplicateMemoryRejections > 0 {
		report.Status = "attention"
	}
	return report, nil
}

func readAdjudicationDecisions(path string) ([]AdjudicationDecision, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var out []AdjudicationDecision
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var d AdjudicationDecision
		if err := json.Unmarshal([]byte(line), &d); err != nil {
			return nil, fmt.Errorf("parse adjudication row %q: %w", line, err)
		}
		out = append(out, d)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func readNoPromoteDecisions(path string) ([]writeback.NoPromoteDecision, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var out []writeback.NoPromoteDecision
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var d writeback.NoPromoteDecision
		if err := json.Unmarshal([]byte(line), &d); err != nil {
			return nil, fmt.Errorf("parse no-promote row %q: %w", line, err)
		}
		out = append(out, d)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func countCandidateFiles(homePath, kind string) (int, error) {
	rel := candidates.InboxDir(candidates.Kind(kind))
	if rel == "" {
		return 0, fmt.Errorf("candidate kind %q has no inbox mapping", kind)
	}
	files, err := filepath.Glob(filepath.Join(homePath, rel, "*.json"))
	if err != nil {
		return 0, err
	}
	return len(files), nil
}

func statusForPositive(n int) string {
	if n > 0 {
		return "observed"
	}
	return "empty"
}

func statusForDebt(n int) string {
	if n > 0 {
		return "attention"
	}
	return "ok"
}
