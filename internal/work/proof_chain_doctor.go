package work

import (
	"strings"
	"time"

	"compound/internal/envelope"
)

const ProofChainDoctorSchemaVersion = "compound.work_proof_chain_doctor.v0.1"

type ProofChainDoctorOptions struct {
	IncludeArchived bool
}

type ProofChainDoctorReport struct {
	SchemaVersion string                    `json:"schema_version"`
	Status        string                    `json:"status"`
	GeneratedAt   string                    `json:"generated_at"`
	Counts        ProofChainDoctorCounts    `json:"counts"`
	Items         []ProofChainDoctorItem    `json:"items,omitempty"`
	Findings      []ProofChainDoctorFinding `json:"findings,omitempty"`
	ProofRefs     []string                  `json:"proof_refs"`
}

type ProofChainDoctorCounts struct {
	WorksChecked        int `json:"works_checked"`
	TerminalChecked     int `json:"terminal_checked"`
	Complete            int `json:"complete"`
	MissingReceipt      int `json:"missing_receipt"`
	MissingTransition   int `json:"missing_transition"`
	InvalidReceipt      int `json:"invalid_receipt"`
	InvalidTransition   int `json:"invalid_transition"`
	MismatchedWorkID    int `json:"mismatched_work_id"`
	MismatchedLink      int `json:"mismatched_link"`
	MissingReceiptAfter int `json:"missing_receipt_after_ref"`
}

type ProofChainDoctorItem struct {
	WorkID        string   `json:"work_id"`
	State         string   `json:"state"`
	ReceiptID     string   `json:"receipt_id,omitempty"`
	TransitionID  string   `json:"transition_id,omitempty"`
	ProofComplete bool     `json:"proof_complete"`
	Problems      []string `json:"problems,omitempty"`
}

type ProofChainDoctorFinding struct {
	WorkID  string `json:"work_id"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (s *Store) ProofChainDoctor(now time.Time, opts ProofChainDoctorOptions) (ProofChainDoctorReport, error) {
	if now.IsZero() {
		now = time.Now()
	}
	works, err := s.List()
	if opts.IncludeArchived {
		works, err = s.ListWithArchived()
	}
	if err != nil {
		return ProofChainDoctorReport{}, err
	}
	report := ProofChainDoctorReport{
		SchemaVersion: ProofChainDoctorSchemaVersion,
		Status:        "ok",
		GeneratedAt:   now.UTC().Format(time.RFC3339),
		ProofRefs:     []string{"work-proof-chain-terminal-receipt", "work-proof-chain-terminal-transition", "work-proof-chain-work-id-match"},
	}
	report.Counts.WorksChecked = len(works)
	for _, w := range works {
		if !terminalProofRequired(w.State) {
			continue
		}
		report.Counts.TerminalChecked++
		item := s.proofChainItem(w, &report)
		if len(item.Problems) == 0 {
			item.ProofComplete = true
			report.Counts.Complete++
		}
		report.Items = append(report.Items, item)
	}
	if len(report.Findings) > 0 {
		report.Status = "fail"
	}
	return report, nil
}

func (s *Store) proofChainItem(w envelope.WorkEnvelope, report *ProofChainDoctorReport) ProofChainDoctorItem {
	events, err := s.eventsFor(w.ID)
	item := ProofChainDoctorItem{WorkID: w.ID, State: w.State}
	if err != nil {
		addProofChainProblem(report, &item, "events_unreadable", "lifecycle events unreadable: "+err.Error())
		return item
	}
	item.ReceiptID = lastAttachedWithPrefix(events, "rec_")
	item.TransitionID = firstNonEmpty(w.IDs.TransitionID, lastAttachedWithPrefix(events, "tr_"))
	if item.ReceiptID == "" {
		report.Counts.MissingReceipt++
		addProofChainProblem(report, &item, "missing_receipt", "terminal work has no closing receipt ref")
	}
	if item.TransitionID == "" {
		report.Counts.MissingTransition++
		addProofChainProblem(report, &item, "missing_transition", "terminal work has no closing transition ref")
	}

	var rec envelope.Receipt
	receiptValid := false
	if item.ReceiptID != "" {
		if err := s.readJSON(s.envPath(item.ReceiptID), &rec); err != nil {
			report.Counts.InvalidReceipt++
			addProofChainProblem(report, &item, "receipt_unreadable", "receipt "+item.ReceiptID+" unreadable: "+err.Error())
		} else if err := rec.Validate(); err != nil {
			report.Counts.InvalidReceipt++
			addProofChainProblem(report, &item, "receipt_invalid", "receipt "+item.ReceiptID+" invalid: "+err.Error())
		} else {
			receiptValid = true
			if rec.IDs.WorkID != w.ID {
				report.Counts.MismatchedWorkID++
				addProofChainProblem(report, &item, "receipt_work_mismatch", "receipt "+item.ReceiptID+" belongs to work "+rec.IDs.WorkID)
			}
		}
	}

	if item.TransitionID != "" {
		var tr envelope.Transition
		if err := s.readJSON(s.envPath(item.TransitionID), &tr); err != nil {
			report.Counts.InvalidTransition++
			addProofChainProblem(report, &item, "transition_unreadable", "transition "+item.TransitionID+" unreadable: "+err.Error())
		} else if err := tr.Validate(); err != nil {
			report.Counts.InvalidTransition++
			addProofChainProblem(report, &item, "transition_invalid", "transition "+item.TransitionID+" invalid: "+err.Error())
		} else {
			if tr.IDs.WorkID != w.ID {
				report.Counts.MismatchedWorkID++
				addProofChainProblem(report, &item, "transition_work_mismatch", "transition "+item.TransitionID+" belongs to work "+tr.IDs.WorkID)
			}
			if strings.TrimSpace(w.IDs.TransitionID) != "" && w.IDs.TransitionID != item.TransitionID {
				report.Counts.MismatchedLink++
				addProofChainProblem(report, &item, "work_transition_link_mismatch", "work transition_id "+w.IDs.TransitionID+" differs from closing transition "+item.TransitionID)
			}
			if receiptValid && !proofChainContains(tr.AfterRefs, rec.ID) {
				report.Counts.MissingReceiptAfter++
				addProofChainProblem(report, &item, "transition_missing_receipt_ref", "transition "+item.TransitionID+" after_refs does not include receipt "+rec.ID)
			}
		}
	}
	return item
}

func addProofChainProblem(report *ProofChainDoctorReport, item *ProofChainDoctorItem, code, message string) {
	item.Problems = append(item.Problems, code)
	report.Findings = append(report.Findings, ProofChainDoctorFinding{WorkID: item.WorkID, Code: code, Message: message})
}

func proofChainContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
