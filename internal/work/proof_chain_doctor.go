package work

import (
	"strings"
	"time"

	"compound/internal/envelope"
	"compound/internal/eventstore"
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
	WorksChecked               int `json:"works_checked"`
	TerminalChecked            int `json:"terminal_checked"`
	Complete                   int `json:"complete"`
	MissingReceipt             int `json:"missing_receipt"`
	MissingTransition          int `json:"missing_transition"`
	MissingActionIntent        int `json:"missing_action_intent"`
	MissingPolicyDecision      int `json:"missing_policy_decision"`
	InvalidReceipt             int `json:"invalid_receipt"`
	InvalidTransition          int `json:"invalid_transition"`
	InvalidActionIntent        int `json:"invalid_action_intent"`
	InvalidPolicyDecision      int `json:"invalid_policy_decision"`
	MismatchedWorkID           int `json:"mismatched_work_id"`
	MismatchedLink             int `json:"mismatched_link"`
	MismatchedActionPolicy     int `json:"mismatched_action_policy"`
	MissingReceiptAfter        int `json:"missing_receipt_after_ref"`
	MissingTransitionActionRef int `json:"missing_transition_action_ref"`
	OrphanReceipt              int `json:"orphan_receipt"`
	OrphanTransition           int `json:"orphan_transition"`
	OrphanActionIntent         int `json:"orphan_action_intent"`
	OrphanPolicyDecision       int `json:"orphan_policy_decision"`
}

type ProofChainDoctorItem struct {
	WorkID           string   `json:"work_id"`
	State            string   `json:"state"`
	ActionIntentIDs  []string `json:"action_intent_ids,omitempty"`
	PolicyDecisionID string   `json:"policy_decision_id,omitempty"`
	ReceiptID        string   `json:"receipt_id,omitempty"`
	TransitionID     string   `json:"transition_id,omitempty"`
	ProofComplete    bool     `json:"proof_complete"`
	Problems         []string `json:"problems,omitempty"`
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
		ProofRefs: []string{
			"work-proof-chain-terminal-receipt",
			"work-proof-chain-terminal-transition",
			"work-proof-chain-work-id-match",
			"work-proof-chain-action-intent",
			"work-proof-chain-policy-decision",
			"work-proof-chain-transition-action-refs",
			"work-proof-chain-orphan-envelope-scan",
		},
	}
	report.Counts.WorksChecked = len(works)
	// One events/work.jsonl read for the whole doctor: eventsByWorkID decodes the
	// full lifecycle log once under the work-event lock and groups by work_id
	// (eventsByWork[id] is identical to a per-work eventsFor slice — same filter
	// and order, see decodeAdmissionEvents). The prior shape re-read+re-decoded
	// the whole log once per terminal item (proofChainItem) PLUS once per work
	// item (the orphan scan), so the doctor was O(works x log size). This mirrors
	// the workbench.go batch seam.
	eventsByWork, err := s.eventsByWorkID()
	if err != nil {
		return ProofChainDoctorReport{}, err
	}
	for _, w := range works {
		if !terminalProofRequired(w.State) {
			continue
		}
		report.Counts.TerminalChecked++
		item := s.proofChainItem(w, eventsByWork[w.ID], &report)
		if len(item.Problems) == 0 {
			item.ProofComplete = true
			report.Counts.Complete++
		}
		report.Items = append(report.Items, item)
	}
	s.addOrphanEnvelopeFindings(works, eventsByWork, &report)
	if len(report.Findings) > 0 {
		report.Status = "fail"
	}
	return report, nil
}

func (s *Store) proofChainItem(w envelope.WorkEnvelope, events []Event, report *ProofChainDoctorReport) ProofChainDoctorItem {
	item := ProofChainDoctorItem{WorkID: w.ID, State: w.State}
	// events is the batched eventsByWorkID slice for this work_id; a malformed
	// log already failed the whole doctor at the batch read, so there is no
	// per-item events_unreadable path.
	item.ReceiptID = lastAttachedWithPrefix(events, "rec_")
	item.TransitionID = firstNonEmpty(w.IDs.TransitionID, lastAttachedWithPrefix(events, "tr_"))
	item.ActionIntentIDs = compactPrefix(w.ActionIntentIDs, "act_")
	item.PolicyDecisionID = strings.TrimSpace(w.PolicyDecisionID)
	if len(item.ActionIntentIDs) == 0 {
		report.Counts.MissingActionIntent++
		addProofChainProblem(report, &item, "missing_action_intent", "terminal work has no action intent run ref")
	}
	if item.PolicyDecisionID == "" {
		report.Counts.MissingPolicyDecision++
		addProofChainProblem(report, &item, "missing_policy_decision", "terminal work has no policy decision ref")
	}
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
		if err := s.readEventJSON(item.ReceiptID, &rec); err != nil {
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

	actionIDs := map[string]bool{}
	for _, actionID := range item.ActionIntentIDs {
		actionIDs[actionID] = true
		var ai envelope.ActionIntent
		if err := s.readEventJSON(actionID, &ai); err != nil {
			report.Counts.InvalidActionIntent++
			addProofChainProblem(report, &item, "action_intent_unreadable", "action intent "+actionID+" unreadable: "+err.Error())
		} else if err := ai.Validate(); err != nil {
			report.Counts.InvalidActionIntent++
			addProofChainProblem(report, &item, "action_intent_invalid", "action intent "+actionID+" invalid: "+err.Error())
		} else if ai.IDs.WorkID != w.ID {
			report.Counts.MismatchedWorkID++
			addProofChainProblem(report, &item, "action_intent_work_mismatch", "action intent "+actionID+" belongs to work "+ai.IDs.WorkID)
		}
	}

	if item.PolicyDecisionID != "" {
		var pd envelope.PolicyDecision
		if err := s.readEventJSON(item.PolicyDecisionID, &pd); err != nil {
			report.Counts.InvalidPolicyDecision++
			addProofChainProblem(report, &item, "policy_decision_unreadable", "policy decision "+item.PolicyDecisionID+" unreadable: "+err.Error())
		} else if err := pd.Validate(); err != nil {
			report.Counts.InvalidPolicyDecision++
			addProofChainProblem(report, &item, "policy_decision_invalid", "policy decision "+item.PolicyDecisionID+" invalid: "+err.Error())
		} else {
			if pd.IDs.WorkID != w.ID {
				report.Counts.MismatchedWorkID++
				addProofChainProblem(report, &item, "policy_decision_work_mismatch", "policy decision "+item.PolicyDecisionID+" belongs to work "+pd.IDs.WorkID)
			}
			if !actionIDs[pd.ActionIntentID] {
				report.Counts.MismatchedActionPolicy++
				addProofChainProblem(report, &item, "policy_action_mismatch", "policy decision "+item.PolicyDecisionID+" references action "+pd.ActionIntentID+" not present in work action intent ids")
			}
		}
	}

	if item.TransitionID != "" {
		var tr envelope.Transition
		if err := s.readEventJSON(item.TransitionID, &tr); err != nil {
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
			for _, actionID := range item.ActionIntentIDs {
				if !proofChainContains(tr.ActionRefs, actionID) {
					report.Counts.MissingTransitionActionRef++
					addProofChainProblem(report, &item, "transition_missing_action_ref", "transition "+item.TransitionID+" action_refs does not include action "+actionID)
				}
			}
		}
	}
	return item
}

func (s *Store) addOrphanEnvelopeFindings(works []envelope.WorkEnvelope, eventsByWork map[string][]Event, report *ProofChainDoctorReport) {
	referenced := map[string]bool{}
	for _, w := range works {
		for _, ref := range w.ActionIntentIDs {
			addProofChainRef(referenced, ref)
		}
		addProofChainRef(referenced, w.PolicyDecisionID)
		addProofChainRef(referenced, w.IDs.TransitionID)
		for _, event := range eventsByWork[w.ID] {
			for _, ref := range event.Attached {
				addProofChainRef(referenced, ref)
			}
		}
	}

	entries, err := s.events.Scan(eventstore.ScanOptions{})
	if err != nil {
		report.Findings = append(report.Findings, ProofChainDoctorFinding{Code: "envelopes_unreadable", Message: "could not scan envelope root: " + err.Error()})
		return
	}
	for _, entry := range entries {
		id := entry.ID
		if referenced[id] {
			continue
		}
		switch {
		case strings.HasPrefix(id, "act_"):
			report.Counts.OrphanActionIntent++
			report.Findings = append(report.Findings, ProofChainDoctorFinding{WorkID: s.envelopeWorkID(id), Code: "orphan_action_intent", Message: "action intent " + id + " is not referenced by any work action intent ids or lifecycle event"})
		case strings.HasPrefix(id, "pol_"):
			report.Counts.OrphanPolicyDecision++
			report.Findings = append(report.Findings, ProofChainDoctorFinding{WorkID: s.envelopeWorkID(id), Code: "orphan_policy_decision", Message: "policy decision " + id + " is not referenced by any work policy_decision_id or lifecycle event"})
		case strings.HasPrefix(id, "rec_"):
			report.Counts.OrphanReceipt++
			report.Findings = append(report.Findings, ProofChainDoctorFinding{WorkID: s.envelopeWorkID(id), Code: "orphan_receipt", Message: "receipt " + id + " is not referenced by any terminal lifecycle event"})
		case strings.HasPrefix(id, "tr_"):
			report.Counts.OrphanTransition++
			report.Findings = append(report.Findings, ProofChainDoctorFinding{WorkID: s.envelopeWorkID(id), Code: "orphan_transition", Message: "transition " + id + " is not referenced by any work transition_id or lifecycle event"})
		default:
			continue
		}
	}
}

func addProofChainRef(refs map[string]bool, ref string) {
	ref = strings.TrimSpace(ref)
	if ref != "" {
		refs[ref] = true
	}
}

func (s *Store) envelopeWorkID(id string) string {
	switch {
	case strings.HasPrefix(id, "act_"):
		var ai envelope.ActionIntent
		if err := s.readEventJSON(id, &ai); err == nil {
			return ai.IDs.WorkID
		}
	case strings.HasPrefix(id, "pol_"):
		var pd envelope.PolicyDecision
		if err := s.readEventJSON(id, &pd); err == nil {
			return pd.IDs.WorkID
		}
	case strings.HasPrefix(id, "rec_"):
		var rec envelope.Receipt
		if err := s.readEventJSON(id, &rec); err == nil {
			return rec.IDs.WorkID
		}
	case strings.HasPrefix(id, "tr_"):
		var tr envelope.Transition
		if err := s.readEventJSON(id, &tr); err == nil {
			return tr.IDs.WorkID
		}
	}
	return ""
}

func compactPrefix(values []string, prefix string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || !strings.HasPrefix(value, prefix) || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
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
